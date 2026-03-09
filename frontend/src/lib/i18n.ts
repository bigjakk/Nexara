import i18n from "i18next";
import { initReactI18next } from "react-i18next";

// Import English translations
import commonEN from "@/locales/en/common.json";
import navigationEN from "@/locales/en/navigation.json";
import authEN from "@/locales/en/auth.json";
import dashboardEN from "@/locales/en/dashboard.json";
import settingsEN from "@/locales/en/settings.json";
import adminEN from "@/locales/en/admin.json";
import clustersEN from "@/locales/en/clusters.json";
import inventoryEN from "@/locales/en/inventory.json";
import vmsEN from "@/locales/en/vms.json";
import storageEN from "@/locales/en/storage.json";
import backupEN from "@/locales/en/backup.json";
import alertsEN from "@/locales/en/alerts.json";
import securityEN from "@/locales/en/security.json";
import topologyEN from "@/locales/en/topology.json";
import consoleEN from "@/locales/en/console.json";
import reportsEN from "@/locales/en/reports.json";
import auditEN from "@/locales/en/audit.json";
import cephEN from "@/locales/en/ceph.json";
import networksEN from "@/locales/en/networks.json";

export const defaultNS = "common";

export const resources = {
  en: {
    common: commonEN,
    navigation: navigationEN,
    auth: authEN,
    dashboard: dashboardEN,
    settings: settingsEN,
    admin: adminEN,
    clusters: clustersEN,
    inventory: inventoryEN,
    vms: vmsEN,
    storage: storageEN,
    backup: backupEN,
    alerts: alertsEN,
    security: securityEN,
    topology: topologyEN,
    console: consoleEN,
    reports: reportsEN,
    audit: auditEN,
    ceph: cephEN,
    networks: networksEN,
  },
} as const;

export const supportedLanguages = [
  { code: "en", name: "English", nativeName: "English" },
] as const;

void i18n.use(initReactI18next).init({
  resources,
  lng: localStorage.getItem("nexara-language") ?? "en",
  fallbackLng: "en",
  defaultNS,
  ns: Object.keys(resources.en),
  interpolation: {
    escapeValue: false, // React already escapes
  },
});

export default i18n;
