import http from "k6/http";
import { check, sleep } from "k6";

const BASE = "http://localhost:8080";
const NODE_ID = __ENV.NODE_ID || "sn-bench";
const GP_POOL_SIZE = 40;
const D_POOL_SIZE = 10;
const PACE_S = Number(__ENV.PACE_S) || 1;
const THROTTLE_POLL_S = Number(__ENV.THROTTLE_POLL_S) || 3; // copies heartbeat interval

// NOTE: all thresholds report under 3ms (~2-3ms), HTTP overhead / JSON marshalling is the taking up most if not all of latency time,
// even if store.Get is faster than store.Set w/ the RLOCK
export const options = {
  thresholds: {
    // NOTE: folding a solution to the tail-latency from mutex contention into RAFT.
    // currently ~3 seconds, brief stall of concurrent readers all release, therefore p(99) < 30ms ~avg 20ms.
    // we want to keep latency around the same for the RAFT implementation if not better (starting with sn RAFT)
    "http_req_duration{name:GetKV}": ["p(50)<5", "p(99)<30"],
    "http_req_duration{name:PutKV}": ["p(50)<6", "p(99)<30"],
    "http_req_duration{name:DeleteKV}": ["p(50)<6", "p(99)<30"],
  },
  scenarios: {
    data_plane: {
      executor: "constant-vus",
      exec: "dataPlane",
      vus: 20,
      duration: "100s",
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
}

export function dataPlane() {
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

  sleep(PACE_S);
}

export function controlPlane() {
  const res = http.get(`${BASE}/throttled`);
  check(res, {
    "GET /throttled: Ensure throttled stays available": (res) =>
      res.status == 200,
    "GET /throttled: Node has not been throttled": (res) =>
      !(NODE_ID in res.json()),
  });

  sleep(THROTTLE_POLL_S);
}
