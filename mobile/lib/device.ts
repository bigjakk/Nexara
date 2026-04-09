/**
 * Device metadata used to tag sessions + register for push. Device ID is
 * generated once and persisted to SecureStore; device name is derived from
 * expo-device.
 */

import * as Crypto from "expo-crypto";
import * as Device from "expo-device";
import { Platform } from "react-native";

import { secureStorage } from "./secure-storage";

export type DevicePlatform = "ios" | "android";

export interface DeviceInfo {
  id: string;
  name: string;
  platform: DevicePlatform;
}

export async function getDeviceInfo(): Promise<DeviceInfo> {
  let id = await secureStorage.getDeviceId();
  if (!id) {
    id = Crypto.randomUUID();
    await secureStorage.setDeviceId(id);
  }

  const brand = Device.brand ?? "";
  const model = Device.modelName ?? "";
  const name = [brand, model].filter(Boolean).join(" ") || "Unknown Device";

  const platform: DevicePlatform = Platform.OS === "ios" ? "ios" : "android";

  return { id, name, platform };
}
