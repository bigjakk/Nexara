/**
 * Auto-lock hook: flips the auth status from `authed` → `locked` when the
 * user has biometric unlock enabled and the app has been backgrounded for
 * more than AUTO_LOCK_MS. The lock screen then handles the biometric prompt
 * loop via the existing `authenticateBiometric` wrapper.
 *
 * Why a dedicated hook:
 *   Expo's AppState API is the only signal for "app went to background". We
 *   record a timestamp on background AND persist it to SecureStore (per
 *   security review M1 — Android low-memory kills wipe in-memory refs but
 *   the timestamp must survive so a 5-minutes-backgrounded → killed →
 *   relaunched session still locks). On the next foreground (or on
 *   bootstrap if the app was killed), we compare elapsed time against the
 *   threshold and call `lock()` on the auth store, which triggers AuthGate
 *   to route to `/(auth)/locked`.
 *
 * Why only when status === "authed":
 *   There's nothing to lock otherwise. The locked / logged_out / loading
 *   states are already on the right screen. Watching AppState constantly
 *   would also waste listener subscriptions.
 *
 * Why check `isBiometricEnrolled` before flipping:
 *   Users who've explicitly turned biometric OFF in Settings should not get
 *   sent to the lock screen at all. Our `authenticateBiometric` wrapper
 *   no-ops when no biometric is enrolled, so they wouldn't actually be gated
 *   — but stranding them on a "Locked" screen with an unrecoverable prompt
 *   would be a confusing UX.
 */

import { useEffect } from "react";
import { AppState, type AppStateStatus } from "react-native";

import { secureStorage } from "@/lib/secure-storage";
import { useAuthStore } from "@/stores/auth-store";

const AUTO_LOCK_MS = 5 * 60 * 1000;

export function useAutoLock(): void {
  const status = useAuthStore((s) => s.status);
  const lock = useAuthStore((s) => s.lock);

  useEffect(() => {
    if (status !== "authed") {
      // Outside the authed state nothing to lock; clear any stale
      // timestamp so a future authed session starts fresh.
      void secureStorage.clearBackgroundedAt();
      return;
    }

    // On mount of an authed session, check whether a backgrounded
    // timestamp from a PREVIOUS process lifecycle (i.e. Android killed
    // the app while backgrounded, and we just bootstrapped from
    // SecureStore) exceeds the threshold. This is the M1 fix — without
    // it, low-memory kills silently bypass auto-lock because the
    // in-memory state used to live in a useRef.
    void (async () => {
      const stored = await secureStorage.getBackgroundedAt();
      if (stored === null) return;
      const elapsed = Date.now() - stored;
      if (elapsed < AUTO_LOCK_MS) {
        // Still within the window, but the timestamp is stale relative
        // to the new foreground — clear it. The next background will
        // record a fresh one.
        await secureStorage.clearBackgroundedAt();
        return;
      }
      const enrolled = await secureStorage.isBiometricEnrolled();
      if (enrolled) {
        lock();
      }
      // Either way, clear the stored timestamp so we don't re-fire.
      await secureStorage.clearBackgroundedAt();
    })();

    const handleChange = (next: AppStateStatus) => {
      if (next === "background") {
        // Persist to SecureStore so the timestamp survives Android
        // low-memory kills. Fire-and-forget — the await ordering vs
        // the OS killing the process is a coin flip, but expo-secure-
        // store is synchronous-enough on the native side that this
        // typically lands before the kill.
        void secureStorage.setBackgroundedAt(Date.now());
        return;
      }
      if (next === "active") {
        void (async () => {
          const stored = await secureStorage.getBackgroundedAt();
          await secureStorage.clearBackgroundedAt();
          if (stored === null) return;
          const elapsed = Date.now() - stored;
          if (elapsed < AUTO_LOCK_MS) return;

          const enrolled = await secureStorage.isBiometricEnrolled();
          if (enrolled) {
            lock();
          }
        })();
      }
      // "inactive" is a transient state (notification shade, control center,
      // task switcher) that happens before real backgrounding. Ignoring it
      // avoids false auto-locks when the user briefly pulls the shade.
    };

    const sub = AppState.addEventListener("change", handleChange);
    return () => {
      sub.remove();
    };
  }, [status, lock]);
}
