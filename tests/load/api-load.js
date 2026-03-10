import http from "k6/http";
import { check, sleep } from "k6";

const BASE_URL = __ENV.BASE_URL || "http://localhost:80";
const EMAIL = __ENV.EMAIL || "admin@nexara.local";
const PASSWORD = __ENV.PASSWORD || "changeme";

export const options = {
  stages: [
    { duration: "30s", target: 10 },
    { duration: "1m", target: 50 },
    { duration: "1m", target: 100 },
    { duration: "30s", target: 0 },
  ],
  thresholds: {
    http_req_duration: ["p(95)<100"],
    http_req_failed: ["rate<0.01"],
  },
};

export function setup() {
  const loginRes = http.post(
    `${BASE_URL}/api/v1/auth/login`,
    JSON.stringify({ email: EMAIL, password: PASSWORD }),
    { headers: { "Content-Type": "application/json" } },
  );

  check(loginRes, {
    "login succeeded": (r) => r.status === 200,
  });

  const body = JSON.parse(loginRes.body);
  return { token: body.access_token };
}

export default function (data) {
  const headers = {
    Authorization: `Bearer ${data.token}`,
    "Content-Type": "application/json",
  };

  // Health check.
  const healthRes = http.get(`${BASE_URL}/healthz`);
  check(healthRes, {
    "healthz 200": (r) => r.status === 200,
  });

  // Version endpoint.
  const versionRes = http.get(`${BASE_URL}/api/v1/version`, { headers });
  check(versionRes, {
    "version 200": (r) => r.status === 200,
  });

  // List clusters.
  const clustersRes = http.get(`${BASE_URL}/api/v1/clusters`, { headers });
  check(clustersRes, {
    "clusters 200": (r) => r.status === 200,
  });

  // Audit log.
  const auditRes = http.get(`${BASE_URL}/api/v1/audit-log?limit=20`, {
    headers,
  });
  check(auditRes, {
    "audit-log 200": (r) => r.status === 200,
  });

  // Tasks.
  const tasksRes = http.get(`${BASE_URL}/api/v1/tasks`, { headers });
  check(tasksRes, {
    "tasks 200": (r) => r.status === 200,
  });

  sleep(1);
}
