import http from "k6/http";
import { check } from "k6";

const NODE_ID = __ENV.NODE_ID || "sn-bench";
const BASE = "http://localhost:8080";
const KEYS = ["k1", "k2", "k3"];

export const options = {
  thresholds: {
    http_req_duration: ["p(99)<1000"], // 99% of reqs should be below 1s
  },
  scenarios: {
    average_load: {
      executor: "ramping-vus",
      stages: [
        { duration: "10s", target: 20 },
        { duration: "20s", target: 20 },
        { duration: "5s", target: 0 },
      ],
    },
  },
};

export default function () {
  const r = Math.random();
  if (r < 0.7) {
    check(http.get(`${BASE}/vitals/${NODE_ID}`), {
      "GET /vitals/{node-id}: 200/404": (res) =>
        res.status === 200 || res.status === 404,
    });
  } else if (r < 0.95) {
    check(http.get(`${BASE}/throttled`), {
      "GET /throttled: 200": (res) => res.status === 200,
    });
  } else {
    const key = KEYS[Math.floor(Math.random() * KEYS.length)];
    if (Math.random() < 0.5) {
      check(http.get(`${BASE}/kvstore/${key}`), {
        "GET /kvstore/{key}: 200/404": (res) =>
          res.status === 200 || res.status === 404,
        "GET /kvstore/{key}: valid json when found": (res) => {
          if (res.status !== 200) return true;
          try {
            JSON.parse(res.body);
            return true;
          } catch {
            return false;
          }
        },
      });
    } else {
      const body = JSON.stringify({ value: "v" });
      check(http.put(`${BASE}/kvstore/${key}`, body), {
        "PUT /kvstore/{key}: 204": (res) => res.status === 204,
      });
    }
  }
}
