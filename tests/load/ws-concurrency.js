import ws from "k6/ws";
import http from "k6/http";
import { check, sleep } from "k6";

const BASE_URL = __ENV.BASE_URL || "http://localhost:80";
const WS_URL = __ENV.WS_URL || "ws://localhost:80/ws";
const EMAIL = __ENV.EMAIL || "admin@nexara.local";
const PASSWORD = __ENV.PASSWORD || "changeme";

export const options = {
  stages: [
    { duration: "30s", target: 50 },
    { duration: "1m", target: 200 },
    { duration: "1m", target: 500 },
    { duration: "30s", target: 0 },
  ],
  thresholds: {
    ws_connecting: ["p(95)<500"],
    ws_session_duration: ["p(95)<60000"],
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
  const url = `${WS_URL}?token=${data.token}`;

  const res = ws.connect(url, {}, function (socket) {
    socket.on("open", function () {
      // Subscribe to a test channel.
      socket.send(
        JSON.stringify({
          type: "subscribe",
          channel: "events:global",
        }),
      );
    });

    socket.on("message", function (msg) {
      const parsed = JSON.parse(msg);
      check(parsed, {
        "has type field": (m) => m.type !== undefined,
      });
    });

    socket.on("error", function (e) {
      console.error("ws error:", e);
    });

    // Keep connection open for 10 seconds to simulate a real user.
    sleep(10);
    socket.close();
  });

  check(res, {
    "ws status 101": (r) => r && r.status === 101,
  });
}
