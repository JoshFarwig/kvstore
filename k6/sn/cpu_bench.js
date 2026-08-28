import http from "k6/http";
import { check, sleep } from "k6";
import { Trend, Counter } from "k6/metrics";

const BASE = "http://localhost:8080";
const NODE_ID = __ENV.NODE_ID || "sn-bench";
const KEY = "cpu-bench-key";

http.setResponseCallback(http.expectedStatuses(200, 204, 404, 503));

const timeToRecoverMs = new Trend("time_to_recover_ms", false);
const throttledRequests = new Counter("throttled_requests");
const nodeCPUPercent = new Trend("node_cpu_percent", false);
const nodeMemPercent = new Trend("node_mem_percent", false);

// NOTE: max CPU % hits anywhere from 43% - 58% locally on cpus=2
// never exceeds since inhertly operations do not have much cpu busy work,
// could induce some kind of busy work for portrayal in test scene
export const options = {
  scenarios: {
    cpu_pressure: {
      executor: "ramping-vus",
      exec: "pressure",
      stages: [
        { duration: "20s", target: Number(__ENV.PEAK_VUS) || 200 }, // ramp up, force CPU% over cap
        { duration: "40s", target: Number(__ENV.PEAK_VUS) || 200 }, // hold, confirm throttle trips
        { duration: "20s", target: 5 }, // back off to baseline, watch recovery
        { duration: "40s", target: 5 },
        { duration: "5s", target: 0 },
      ],
    },
    control_plane: {
      executor: "constant-vus",
      exec: "controlPlane",
      vus: 2,
      duration: "125s",
    },
  },
};

export function setup() {
  http.put(`${BASE}/kvstore/${KEY}`, JSON.stringify({ value: "seed" }));
  return { start: Date.now() };
}

let wasThrottled = false;
let recovered = false;

export function pressure(data) {
  const res = http.get(`${BASE}/kvstore/${KEY}`);
  check(res, {
    "GET /kvstore/{key}: 200 or 503 (throttled)": (r) =>
      r.status === 200 || r.status === 503,
  });

  if (res.status === 503) {
    throttledRequests.add(1);
    wasThrottled = true;
  } else if (res.status === 200 && wasThrottled && !recovered) {
    recovered = true;
    timeToRecoverMs.add(Date.now() - data.start);
  }
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
  sleep(1);
}
