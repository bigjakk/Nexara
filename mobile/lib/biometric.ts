/**
 * Biometric auth wrapper around expo-local-authentication. Used as the gate
 * for launching the app when a refresh token is present. If the device has
 * no biometric hardware or the user hasn't enrolled one, the gate degrades
 * to "no gate" — the user still has their refresh token-protected session.
 */

import { Alert as RNAlert } from "react-native";
import * as LocalAuthentication from "expo-local-authentication";

import { secureStorage } from "@/lib/secure-storage";

export interface BiometricCapabilities {
  hasHardware: boolean;
  isEnrolled: boolean;
  supportsFaceID: boolean;
  supportsFingerprint: boolean;
}

export async function getBiometricCapabilities(): Promise<BiometricCapabilities> {
  const [hasHardware, isEnrolled, types] = await Promise.all([
    LocalAuthentication.hasHardwareAsync(),
    LocalAuthentication.isEnrolledAsync(),
    LocalAuthentication.supportedAuthenticationTypesAsync(),
  ]);

  return {
    hasHardware,
    isEnrolled,
    supportsFaceID: types.includes(
      LocalAuthentication.AuthenticationType.FACIAL_RECOGNITION,
    ),
    supportsFingerprint: types.includes(
      LocalAuthentication.AuthenticationType.FINGERPRINT,
    ),
  };
}

/**
 * Prompt the user for biometric auth. Resolves true on success.
 * On devices without biometrics, returns true immediately (no-op gate).
 */
export async function authenticateBiometric(reason: string): Promise<boolean> {
  const caps = await getBiometricCapabilities();
  if (!caps.hasHardware || !caps.isEnrolled) {
    return true;
  }

  const result = await LocalAuthentication.authenticateAsync({
    promptMessage: reason,
    cancelLabel: "Cancel",
    disableDeviceFallback: false,
    fallbackLabel: "Use passcode",
  });

  return result.success;
}

/**
 * After a successful fresh login (password or TOTP verify), offer the
 * user the chance to enable biometric unlock without making them dig
 * into Settings → Security. Shown at most once per install — a "Not
 * now" response is remembered across sign-outs so the user isn't
 * re-pestered on every login.
 *
 * Silent no-op when:
 *   - the device has no biometric hardware, or
 *   - no biometric is enrolled at the OS level, or
 *   - the user has already enabled biometric unlock, or
 *   - the prompt has already been shown once on this install.
 *
 * Fire-and-forget from the caller — does not block navigation. The
 * user sees the native Android alert dialog overlaid on whatever screen
 * the AuthGate navigated them to.
 */
export async function maybePromptBiometricEnrollment(): Promise<void> {
  const [alreadyShown, alreadyEnrolled, caps] = await Promise.all([
    secureStorage.hasBiometricPromptBeenShown(),
    secureStorage.isBiometricEnrolled(),
    getBiometricCapabilities(),
  ]);

  if (alreadyShown) return;
  if (alreadyEnrolled) return;
  if (!caps.hasHardware || !caps.isEnrolled) return;

  // Mark the prompt as shown immediately — if the user tap-outside-
  // dismisses the dialog or the JS runtime is killed before they decide,
  // we still count this as "we asked". They can always enable later in
  // Settings.
  await secureStorage.setBiometricPromptShown(true);

  const featureLabel = caps.supportsFaceID
    ? "Face ID / fingerprint"
    : caps.supportsFingerprint
      ? "your fingerprint"
      : "biometric unlock";

  RNAlert.alert(
    "Enable biometric unlock?",
    `Use ${featureLabel} to reopen Nexara without typing your password every time. You can change this anytime in Settings → Security.`,
    [
      { text: "Not now", style: "cancel" },
      {
        text: "Enable",
        onPress: () => {
          void secureStorage.setBiometricEnrolled(true);
        },
      },
    ],
  );
}
