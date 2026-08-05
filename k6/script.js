import http from "k6/http";
import { sleep, check } from "k6";

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
    high_load: {},
  },
};

export default function () {
  let res = http.get("https://quickpizza.grafana.com");
  check(res, { "status is 200": (res) => res.status === 200 });
  sleep(1);
}
