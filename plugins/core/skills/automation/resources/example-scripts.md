# automation — example scripts

## Example 1: restart-device-connection runbook

**Manual runbook:**
1. Check device-gateway logs for the disconnected device
2. Restart the device-gateway pod
3. Verify the device reconnects

**Automated script (saved to `devops/scripts/restart-device-connection.sh`):**

```bash
#!/usr/bin/env bash
# restart-device-connection.sh
# Restarts the device-gateway deployment to recover a disconnected device.
#
# Usage: ./restart-device-connection.sh --namespace <ns> --deployment <deploy> [--dry-run]
# Example: ./restart-device-connection.sh --namespace app --deployment device-gateway

set -euo pipefail

NAMESPACE=""
DEPLOYMENT=""
DRY_RUN=false

usage() {
  echo "Usage: $0 --namespace <ns> --deployment <deploy> [--dry-run]"
  exit 1
}

while [[ $# -gt 0 ]]; do
  case $1 in
    --namespace) NAMESPACE="$2"; shift 2 ;;
    --deployment) DEPLOYMENT="$2"; shift 2 ;;
    --dry-run) DRY_RUN=true; shift ;;
    *) usage ;;
  esac
done

[[ -z "$NAMESPACE" || -z "$DEPLOYMENT" ]] && usage

log() { echo "[$(date +'%Y-%m-%d %H:%M:%S')] $1"; }

log "Checking pods for deployment/$DEPLOYMENT in namespace $NAMESPACE..."
kubectl get pods -n "$NAMESPACE" -l "app=$DEPLOYMENT" --no-headers

log "Dry-run: rollout restart deployment/$DEPLOYMENT -n $NAMESPACE"
kubectl rollout restart "deployment/$DEPLOYMENT" -n "$NAMESPACE" --dry-run=client

if $DRY_RUN; then
  log "DRY RUN complete. No changes made."
  exit 0
fi

read -rp "Proceed with restart? (y/N): " confirm
[[ "$confirm" != "y" && "$confirm" != "Y" ]] && { log "Aborted."; exit 0; }

log "Restarting deployment/$DEPLOYMENT in namespace $NAMESPACE..."
kubectl rollout restart "deployment/$DEPLOYMENT" -n "$NAMESPACE"

log "Waiting for rollout to complete..."
kubectl rollout status "deployment/$DEPLOYMENT" -n "$NAMESPACE" --timeout=120s

log "Post-restart pod status:"
kubectl get pods -n "$NAMESPACE" -l "app=$DEPLOYMENT" --no-headers

log "Restart complete."

# Rollback: kubectl rollout undo deployment/$DEPLOYMENT -n $NAMESPACE
```

## Example 2: chaos-kill-pod experiment

```bash
#!/usr/bin/env bash
# chaos-kill-pod.sh
# Kills a random pod in the target deployment to test resilience.
#
# Usage: ALLOW_CHAOS=true ./chaos-kill-pod.sh --namespace <ns> --deployment <deploy>
# Safety: blocked in production namespaces; requires ALLOW_CHAOS=true

set -euo pipefail

NAMESPACE=""
DEPLOYMENT=""

while [[ $# -gt 0 ]]; do
  case $1 in
    --namespace) NAMESPACE="$2"; shift 2 ;;
    --deployment) DEPLOYMENT="$2"; shift 2 ;;
    *) echo "Unknown arg: $1"; exit 1 ;;
  esac
done

[[ -z "$NAMESPACE" || -z "$DEPLOYMENT" ]] && { echo "Missing required args"; exit 1; }

if [[ "${ALLOW_CHAOS:-}" != "true" ]]; then
  echo "ERROR: ALLOW_CHAOS=true is required."
  exit 1
fi

if [[ "$NAMESPACE" == *prod* || "$NAMESPACE" == *production* ]]; then
  echo "ERROR: Chaos experiments are blocked in production namespaces ('$NAMESPACE')."
  exit 1
fi

log() { echo "[$(date +'%Y-%m-%d %H:%M:%S')] $1"; }

POD=$(kubectl get pods -n "$NAMESPACE" -l "app=$DEPLOYMENT" \
  --field-selector=status.phase=Running -o jsonpath='{.items[0].metadata.name}')

if [[ -z "$POD" ]]; then
  log "No running pods found for deployment/$DEPLOYMENT in $NAMESPACE"
  exit 1
fi

log "Target pod: $POD"
log "Deleting pod $POD in namespace $NAMESPACE..."
kubectl delete pod "$POD" -n "$NAMESPACE"

log "Waiting for replacement pod..."
kubectl rollout status "deployment/$DEPLOYMENT" -n "$NAMESPACE" --timeout=60s

log "Post-chaos pod status:"
kubectl get pods -n "$NAMESPACE" -l "app=$DEPLOYMENT" --no-headers

log "Chaos experiment complete. Verify telemetry and connectivity."
```
