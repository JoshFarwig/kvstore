import http from "k6/http";
import { check, sleep } from "k6";
import { Trend, Counter } from "k6/metrics";

const BASE = "http://localhost:8080";
const NODE_ID = __ENV.NODE_ID || "sn-bench";
const GP_POOL_SIZE = 40;
const D_POOL_SIZE = 10;
const THROTTLE_POLL_S = Number(__ENV.THROTTLE_POLL_S) || 3;
const PEAK_VUS = Number(__ENV.PEAK_VUS) || 500;

http.setResponseCallback(http.expectedStatuses(200, 204, 404, 503));

const timeToThrottleMs = new Trend("time_to_throttle_ms", false);
const throttledRequests = new Counter("throttled_requests");
const nodeCPUPercent = new Trend("node_cpu_percent", false);
const nodeMemPercent = new Trend("node_mem_percent", false);

// NOTE: http request cap ~37k/s until latency starts growing,
// cpu and mem barely reached maximums, hovers ~43% cpu 1% mem at 2cpus, 1.5g mem
export const options = {
  scenarios: {
    data_plane: {
      executor: "ramping-vus",
      exec: "dataPlane",
      stages: [
        { duration: "30s", target: PEAK_VUS },
        { duration: "60s", target: PEAK_VUS },
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
  for (let i = 0; i < GP_POOL_SIZE; i++) {
    http.put(
      `${BASE}/kvstore/key-gp-${i}`,
      JSON.stringify({ value: "seed PUT/GET" }),
    );
  }
  for (let i = 0; i < D_POOL_SIZE; i++) {
    http.put(
      `${BASE}/kvstore/key-d-${i}`,
      JSON.stringify({ value: "seed DELETE" }),
    );
  }
  return { start: Date.now() };
}

let seenThrottle = false;

export function dataPlane(data) {
  const r = Math.random();
  var res;
  if (r < 0.7) {
    const key = `key-gp-${Math.floor(Math.random() * GP_POOL_SIZE)}`;
    res = http.get(`${BASE}/kvstore/${key}`, { tags: { name: "GetKV" } });
    check(res, {
      "GET kvstore/{key}: 200 value retrieved": (res) => res.status == 200,
    });
  } else if (r < 0.95) {
    const key = `key-gp-${Math.floor(Math.random() * GP_POOL_SIZE)}`;
    res = http.put(
      `${BASE}/kvstore/${key}`,
      JSON.stringify({ value: "new PUT" }),
      { tags: { name: "PutKV" } },
    );
    check(res, {
      "PUT kvstore/{key}: 204 new value in key": (res) => res.status == 204,
    });
  } else {
    const key = `key-d-${Math.floor(Math.random() * D_POOL_SIZE)}`;
    res = http.del(`${BASE}/kvstore/${key}`, null, {
      tags: { name: "DeleteKV" },
    });
    check(res, {
      "DELETE kvstore/{key}: 204 key removed": (res) => res.status == 204,
    });
    res = http.put(
      `${BASE}/kvstore/${key}`,
      JSON.stringify({ value: "seed DELETE" }),
    );
    check(res, {
      "PUT kvstore/{key}: 204 delete pool restored": (res) => res.status == 204,
    });
  }

  if (res.status === 503) {
    throttledRequests.add(1);
    if (!seenThrottle) {
      seenThrottle = true;
      timeToThrottleMs.add(Date.now() - data.start);
    }
  }
}

export function controlPlane() {
  const res = http.get(`${BASE}/throttled`);
  check(res, {
    "GET /throttled: Ensure throttled stays available": (res) =>
      res.status == 200,
    "GET /throttled: Node has not been throttled": (res) =>
      !(NODE_ID in res.json()),
  });

  const vitalsRes = http.get(`${BASE}/vitals/${NODE_ID}`);
  check(vitalsRes, {
    "GET /vitals/{node}: 200 while node is under load": (r) => r.status === 200,
  });
  if (vitalsRes.status === 200) {
    const v = vitalsRes.json().value;
    nodeCPUPercent.add(v.cpuPercent);
    nodeMemPercent.add(v.memPercent);
  }

  sleep(THROTTLE_POLL_S);
}
