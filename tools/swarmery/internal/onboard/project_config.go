package onboard

// WriteProjectConfig merges ONE top-level key into
// <projectDir>/.claude/project.json — the only place the daemon writes a
// project's overlay.
//
// The file is read by humans and by agent-work.sh, not only by this daemon, so
// the merge is surgical in the Attach/TogglePlugin mold (attach.go:132-186,
// toggle.go:35-84): every foreign key survives, IN ITS ORIGINAL POSITION, and
// the result keeps the 2-space indentation the overlays ship with
// (overlays/example/project.json). A map[string]json.RawMessage would lose that
// order — Go map iteration is randomised and json.Marshal sorts keys — so the
// merge runs over an ordered slice decoded token by token instead.
//
// Nothing here knows what any key means. Which keys may be written is decided
// upstream, by the pack declarations internal/pluginreq reads.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var (
	// ErrNoProjectConfig — there is no .claude/project.json to merge into. The
	// daemon does NOT create an overlay on empty ground: that is attach's and
	// scripts/init.sh's job, and a project.json conjured out of one key would be
	// missing every field the rest of the toolchain reads. Callers map this to 409.
	ErrNoProjectConfig = errors.New("no .claude/project.json — attach the project first")
	// ErrBadProjectConfig — project.json is not a valid JSON object; never
	// overwritten. Mirrors ErrBadSettings (toggle.go:25): a file we cannot parse
	// is a file whose foreign keys we cannot promise to preserve.
	ErrBadProjectConfig = errors.New("malformed .claude/project.json")
)

// ConfigWriteResult reports what a WriteProjectConfig call did.
type ConfigWriteResult struct {
	// Backup is where the pre-write copy actually landed: a path relative to
	// projectDir when the backup is reachable under it (the ordinary
	// ".claude/project.json.bak"), otherwise the absolute path beside the symlink
	// TARGET. It is a recovery instruction, so it names the real file.
	Backup string
	// Path is the absolute file the merge actually landed in — the symlink
	// TARGET when .claude (or project.json itself) is a symlinked overlay.
	Path string
	// Changed is false when the key already held byte-identical JSON. The write
	// still happens: normalising indentation on an otherwise-equal value is the
	// one visible effect, and reporting "changed" for it would be a lie.
	Changed bool
}

// configEntry is one top-level key of project.json, kept in file order.
type configEntry struct {
	key string
	raw json.RawMessage
}

// WriteProjectConfig sets project.json's top-level `key` to `value`.
//
// An existing key is replaced IN PLACE; a new one is appended at the end. The
// previous contents are copied to project.json.bak first (an older .bak is
// overwritten — the freshest backup is the useful one, and attach reads exactly
// that). The write itself goes to a temp file in the destination directory and
// is renamed over the target, so an interrupted call can never leave a
// half-written overlay.
func WriteProjectConfig(projectDir, key string, value json.RawMessage) (*ConfigWriteResult, error) {
	if key == "" {
		return nil, fmt.Errorf("config key is required")
	}
	if !json.Valid(value) {
		return nil, fmt.Errorf("value is not valid JSON")
	}

	pjPath := filepath.Join(projectDir, ".claude", "project.json")
	orig, err := os.ReadFile(pjPath) // follows symlinks — the shared-overlay pattern
	if os.IsNotExist(err) {
		return nil, ErrNoProjectConfig
	}
	if err != nil {
		return nil, err
	}

	entries, err := decodeOrdered(orig)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadProjectConfig, err)
	}

	merged, changed := mergeEntry(entries, key, value)
	out, err := encodeOrdered(merged)
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", pjPath, err)
	}

	// Resolve the symlink chain to the real file BEFORE writing: for a
	// multi-repo consumer whose .claude is a symlink into a shared agents repo,
	// the temp file has to be created in the destination directory or the
	// os.Rename below crosses a filesystem boundary and fails (EXDEV).
	realPath, err := filepath.EvalSymlinks(pjPath)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", pjPath, err)
	}
	perm := os.FileMode(0o644)
	if fi, serr := os.Stat(realPath); serr == nil {
		perm = fi.Mode().Perm()
	}

	backupPath := realPath + ".bak"
	if err := os.WriteFile(backupPath, orig, perm); err != nil {
		return nil, fmt.Errorf("write backup %s.bak: %w", realPath, err)
	}
	if err := writeFileAtomic(realPath, out, perm); err != nil {
		return nil, err
	}
	return &ConfigWriteResult{
		Backup:  backupReportPath(projectDir, backupPath),
		Path:    realPath,
		Changed: changed,
	}, nil
}

// backupReportPath renders where the operator will find the .bak this call just
// wrote. The backup always lands beside the REAL file, which for a symlinked
// overlay is not under projectDir at all — a fixed ".claude/project.json.bak"
// would send someone looking in a directory that has no such file. Preference
// order, most useful first:
//
//  1. a path relative to projectDir, when the backup really lives under it (the
//     ordinary overlay, and the shared repo that happens to sit inside the
//     project);
//  2. ".claude/project.json.bak" when that path REACHES the same file through a
//     symlinked .claude directory — the multi-repo overlay, where the short path
//     is both friendlier and correct;
//  3. the absolute real path, when neither holds — project.json itself being the
//     symlink, where nothing under projectDir names the backup.
func backupReportPath(projectDir, realBackup string) string {
	base := projectDir
	// projectDir may itself sit behind a symlink (/var -> /private/var on macOS);
	// comparing an unresolved base against a resolved backup would read as an
	// escape and lose the friendly path for an ordinary overlay.
	if resolved, err := filepath.EvalSymlinks(projectDir); err == nil {
		base = resolved
	}
	if rel, err := filepath.Rel(base, realBackup); err == nil && !escapesDir(rel) {
		return rel
	}
	viaProject := filepath.Join(projectDir, ".claude", "project.json.bak")
	if a, err := os.Stat(viaProject); err == nil {
		if b, berr := os.Stat(realBackup); berr == nil && os.SameFile(a, b) {
			return filepath.Join(".claude", "project.json.bak")
		}
	}
	return realBackup
}

// escapesDir reports whether a relative path climbs out of the directory it was
// computed against.
func escapesDir(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// mergeEntry replaces key in place, or appends it when absent. A duplicate key
// (legal JSON, and something a hand-edited overlay can carry) collapses to the
// first position — leaving the second copy would let the stale value win on the
// next read, which is the opposite of what the operator just asked for.
func mergeEntry(entries []configEntry, key string, value json.RawMessage) (out []configEntry, changed bool) {
	out = make([]configEntry, 0, len(entries)+1)
	replaced := false
	for _, e := range entries {
		if e.key != key {
			out = append(out, e)
			continue
		}
		if replaced {
			changed = true // a duplicate was dropped
			continue
		}
		replaced = true
		if !jsonEqual(e.raw, value) {
			changed = true
		}
		out = append(out, configEntry{key: key, raw: value})
	}
	if !replaced {
		out = append(out, configEntry{key: key, raw: value})
		changed = true
	}
	return out, changed
}

// jsonEqual compares two values by their compacted form, so a reformatted but
// semantically identical value does not read as a change.
func jsonEqual(a, b json.RawMessage) bool {
	var ca, cb bytes.Buffer
	if json.Compact(&ca, a) != nil || json.Compact(&cb, b) != nil {
		return false
	}
	return bytes.Equal(ca.Bytes(), cb.Bytes())
}

// decodeOrdered reads a JSON object into its key/raw pairs, in file order.
//
// Values stay json.RawMessage: decoding them would turn every integer into a
// float64 and reorder every nested object on the way back out, which is exactly
// the damage this function exists to prevent.
func decodeOrdered(data []byte) ([]configEntry, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("top level is not a JSON object")
	}
	var out []configEntry
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := kt.(string)
		if !ok {
			return nil, fmt.Errorf("object key is not a string")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		out = append(out, configEntry{key: key, raw: raw})
	}
	if _, err := dec.Token(); err != nil { // closing '}'
		return nil, err
	}
	return out, nil
}

// encodeOrdered renders the entries back to the overlay's house style: 2-space
// indentation, trailing newline. json.Indent (not MarshalIndent) does the
// layout, because it re-indents the ORIGINAL bytes of every untouched value —
// numbers keep their written form and nested keys keep their order, neither of
// which survives a decode/encode round trip.
func encodeOrdered(entries []configEntry) ([]byte, error) {
	var flat bytes.Buffer
	flat.WriteByte('{')
	for i, e := range entries {
		if i > 0 {
			flat.WriteByte(',')
		}
		k, err := json.Marshal(e.key)
		if err != nil {
			return nil, err
		}
		flat.Write(k)
		flat.WriteByte(':')
		flat.Write(e.raw)
	}
	flat.WriteByte('}')

	var out bytes.Buffer
	if err := json.Indent(&out, flat.Bytes(), "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// writeFileAtomic writes via a temp file in the SAME directory as path, then
// renames. Same-directory is not a detail: os.Rename is only atomic within one
// filesystem, and for a symlinked overlay the destination lives on whatever
// volume the shared repo is on.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	// CreateTemp is 0600; the overlay is read by other tools and by the operator.
	if err := os.Chmod(tmpName, perm); err != nil {
		cleanup()
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("rename %s -> %s: %w", tmpName, path, err)
	}
	return nil
}
