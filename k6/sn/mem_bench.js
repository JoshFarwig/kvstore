import http from "k6/http";
import { check, sleep } from "k6";
import { Trend, Counter } from "k6/metrics";

const BASE = "http://localhost:8080";
const NODE_ID = __ENV.NODE_ID || "sn-bench";

// NOTE: lower PACE_S or raise VALUE_BYTES to hit the cap faster.
const VALUE_BYTES = Number(__ENV.VALUE_BYTES) || 20000;
const PACE_S = Number(__ENV.PACE_S) || 0.02;
const VALUE = JSON.stringify({ value: "x".repeat(VALUE_BYTES) });

http.setResponseCallback(http.expectedStatuses(200, 204, 404, 503));

const timeToThrottleMs = new Trend("time_to_throttle_ms", false);
const throttledRequests = new Counter("throttled_requests");
const nodeCPUPercent = new Trend("node_cpu_percent", false);
const nodeMemPercent = new Trend("node_mem_percent", false);

// NOTE: at 20000 bytes every 20ms at 60% threhsholds, mem avg ~47%, max ~66%.
export const options = {
  scenarios: {
    mem_pressure: {
      executor: "ramping-vus",
      exec: "pressure",
      stages: [
        { duration: "30s", target: 30 },
        { duration: "60s", target: 30 },
        { duration: "10s", target: 0 },
      ],
    },
    control_plane: {
      executor: "constant-vus",
      exec: "controlPlane",
      vus: 2,
      duration: "100s",
    },
  },
};

export function setup() {
  return { start: Date.now() };
}

// every key unique, no ttl, expands till threshhold is hit for mem
let counter = 0;
let seenThrottle = false;

export function pressure(data) {
  const key = `mem-${__VU}-${counter++}`;
  const res = http.put(`${BASE}/kvstore/${key}`, VALUE, {
    tags: { name: "kvstore_put" },
  });
  check(res, {
    "PUT /kvstore/{key}: 204 or 503 (throttled)": (r) =>
      r.status === 204 || r.status === 503,
  });

  if (res.status === 503) {
    throttledRequests.add(1);
    if (!seenThrottle) {
      seenThrottle = true;
      timeToThrottleMs.add(Date.now() - data.start);
    }
  }

  sleep(PACE_S);
}

export function controlPlane() {
  const vitalsRes = http.get(`${BASE}/vitals/${NODE_ID}`);
  check(vitalsRes, {
    "GET /vitals/{node}: 200 while node is under pressure": (r) =>
      r.status === 200,
  });
  if (vitalsRes.status === 200) {
    const v = vitalsRes.json().value;
    nodeCPUPercent.add(v.cpuPercent);
    nodeMemPercent.add(v.memPercent);
  }
  check(http.get(`${BASE}/throttled`), {
    "GET /throttled: 200 while node is under pressure": (r) => r.status === 200,
  });
  check(http.get(`${BASE}/threshold/${NODE_ID}`), {
    "GET /threshold/{node}: 200 while node is under pressure": (r) =>
      r.status === 200,
  });
  sleep(1);
}
