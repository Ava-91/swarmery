# api-integration — key code patterns

Patterns are illustrated with Drizzle ORM — adapt to the project's actual ORM (check `CLAUDE.md` and project.json -> `stack.db`).

## Pattern 1: REST Route Handler (CRUD)

```typescript
// src/app/api/devices/route.ts
import { auth } from '@/lib/auth';
import { getDb } from '@/lib/db';
import { devices } from '@/lib/db/schema';

export const dynamic = 'force-dynamic';

export async function GET() {
  const session = await auth();
  if (!session) return Response.json({ error: 'Unauthorized' }, { status: 401 });
  const allDevices = await getDb().select().from(devices);
  return Response.json(allDevices);
}
```

## Pattern 2: ORM Queries (Drizzle shown)

```typescript
import { getDb } from '@/lib/db';
import { devices, missions, deviceMissions } from '@/lib/db/schema';
import { eq } from 'drizzle-orm';

const activeDevices = await getDb().select().from(devices).where(eq(devices.active, true));

const missionWithDevices = await getDb()
  .select().from(missions)
  .leftJoin(deviceMissions, eq(missions.id, deviceMissions.missionId))
  .leftJoin(devices, eq(deviceMissions.deviceId, devices.id))
  .where(eq(missions.id, missionId));

const [newMission] = await getDb()
  .insert(missions).values({ name: 'Patrol Alpha', type: 'BY_ROUTE' }).returning();
```

## Pattern 3: Lazy Database Initialization

```typescript
// src/lib/db/index.ts -- reference only, do not recreate
let db: ReturnType<typeof drizzle<typeof schema>> | null = null;
declare global { var __db: ReturnType<typeof drizzle<typeof schema>> | undefined; }
export function getDb() {
  if (!db) {
    const pool = new Pool({ connectionString: process.env.DATABASE_URL });
    db = drizzle(pool, { schema });
    if (process.env.NODE_ENV === 'development') globalThis.__db = db;
  }
  return db;
}
```

## Pattern 4: WebSocket Telemetry (Server-Side)

```typescript
// src/lib/telemetry/ws-client.ts
import WebSocket from 'ws';
import { EventEmitter } from 'events';
import { z } from 'zod';

export const telemetryEmitter = new EventEmitter();

// Example GPS/IMU telemetry shape -- replace fields with the project's device telemetry contract
const TelemetrySchema = z.object({
  LATITUDE: z.number(), LONGITUDE: z.number(), ALTITUDE: z.number(),
  RELATIVE_ALTITUDE: z.number(), HEADING: z.number(), GROUND_SPEED: z.number(),
  VERTICAL_SPEED: z.number(), BATTERY_REMAINING: z.number(), SYSTEM_STATUS: z.number(),
  DEVICE_MODE: z.string(), GPS_FIX_TYPE: z.number(), SATELLITES_VISIBLE: z.number(),
  ROLL: z.number(), PITCH: z.number(), YAW: z.number(),
});

export function connectToDevice(deviceId: string, wsUrl: string) {
  const ws = new WebSocket(wsUrl);
  ws.on('message', (data: Buffer) => {
    const parsed = TelemetrySchema.safeParse(JSON.parse(data.toString()));
    if (!parsed.success) { console.error(`Invalid telemetry from ${deviceId}:`, parsed.error.message); return; }
    telemetryEmitter.emit(`telemetry:${deviceId}`, parsed.data);
  });
  ws.on('close', () => { setTimeout(() => connectToDevice(deviceId, wsUrl), 3000); });
  ws.on('error', (err) => { console.error(`WS error for ${deviceId}:`, err.message); });
  return ws;
}
```

## Pattern 5: SSE Streaming (Server)

```typescript
// src/app/api/telemetry/stream/route.ts
import { telemetryEmitter } from '@/lib/telemetry/ws-client';
export const dynamic = 'force-dynamic';

export async function GET(req: Request) {
  const deviceId = new URL(req.url).searchParams.get('deviceId');
  const stream = new ReadableStream({
    start(controller) {
      const handler = (data: unknown) => { controller.enqueue(`data: ${JSON.stringify(data)}\n\n`); };
      telemetryEmitter.on(`telemetry:${deviceId}`, handler);
      req.signal.addEventListener('abort', () => { telemetryEmitter.off(`telemetry:${deviceId}`, handler); controller.close(); });
    },
  });
  return new Response(stream, { headers: { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache', Connection: 'keep-alive' } });
}
```

## Pattern 6: Browser EventSource Hook

```typescript
// src/hooks/useTelemetry.ts
'use client';
import { useEffect, useRef, useState } from 'react';

export function useTelemetry(deviceId: string) {
  const [telemetry, setTelemetry] = useState<Telemetry | null>(null);
  const [connected, setConnected] = useState(false);
  const retryRef = useRef<ReturnType<typeof setTimeout>>();
  useEffect(() => {
    let es: EventSource;
    function connect() {
      es = new EventSource(`/api/telemetry/stream?deviceId=${deviceId}`);
      es.onopen = () => setConnected(true);
      es.onmessage = (event) => setTelemetry(JSON.parse(event.data));
      es.onerror = () => { setConnected(false); es.close(); retryRef.current = setTimeout(connect, 3000); };
    }
    connect();
    return () => { es?.close(); if (retryRef.current) clearTimeout(retryRef.current); };
  }, [deviceId]);
  return { telemetry, connected };
}
```
