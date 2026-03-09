# Contributing Translations to Nexara

Thank you for helping translate Nexara! This guide explains how to add a new language or improve existing translations.

## Directory Structure

```
frontend/src/locales/
├── CONTRIBUTING.md          ← You are here
├── en/                      ← English (base language)
│   ├── common.json          ← Shared UI strings (buttons, labels, status)
│   ├── navigation.json      ← Sidebar navigation labels
│   ├── auth.json            ← Login, register, 2FA
│   ├── dashboard.json       ← Dashboard page and widgets
│   ├── settings.json        ← Appearance and security settings
│   ├── admin.json           ← User, role, LDAP, OIDC, branding admin
│   ├── clusters.json        ← Cluster management
│   ├── inventory.json       ← Inventory page
│   ├── vms.json             ← VM/CT management
│   ├── storage.json         ← Storage management
│   ├── backup.json          ← Backup management
│   ├── alerts.json          ← Alert engine
│   ├── security.json        ← CVE scanning, rolling updates
│   ├── topology.json        ← Topology visualization
│   ├── console.json         ← Console page
│   ├── reports.json         ← Reports page
│   ├── audit.json           ← Audit log
│   ├── ceph.json            ← Ceph storage
│   └── networks.json        ← Network management
```

## Adding a New Language

### Step 1: Copy the English translations

```bash
cp -r frontend/src/locales/en frontend/src/locales/<lang-code>
```

Use standard BCP 47 language codes: `de`, `fr`, `es`, `ja`, `zh`, `pt-BR`, etc.

### Step 2: Translate the JSON files

Edit each JSON file in your new language directory. Translate the **values** only — never change the keys.

```json
// en/common.json
{
  "save": "Save",
  "cancel": "Cancel"
}

// de/common.json
{
  "save": "Speichern",
  "cancel": "Abbrechen"
}
```

### Step 3: Handle interpolation variables

Some strings contain `{{variable}}` placeholders. Keep these exactly as-is in your translations:

```json
// en/auth.json
{
  "signInWith": "Sign in with {{provider}}"
}

// de/auth.json
{
  "signInWith": "Anmelden mit {{provider}}"
}
```

### Step 4: Register the new language

1. **Import translations** in `frontend/src/lib/i18n.ts`:

```typescript
// Add imports for the new language
import commonDE from "@/locales/de/common.json";
import navigationDE from "@/locales/de/navigation.json";
// ... import all namespace files

// Add to resources object
export const resources = {
  en: { /* ... existing */ },
  de: {
    common: commonDE,
    navigation: navigationDE,
    // ... all namespaces
  },
} as const;
```

2. **Add to supported languages** in `frontend/src/lib/i18n.ts`:

```typescript
export const supportedLanguages = [
  { code: "en", name: "English", nativeName: "English" },
  { code: "de", name: "German", nativeName: "Deutsch" },
] as const;
```

That's it! The language will automatically appear in the language selector at Settings > Appearance.

## Translation Guidelines

### Do
- Keep translations concise — UI space is limited
- Preserve all `{{variable}}` placeholders exactly as they appear
- Test your translations in the UI to check for text overflow
- Translate all files in the language directory — partial translations are confusing

### Don't
- Don't change JSON keys — only translate values
- Don't translate technical terms like "VMID", "CIDR", "OSD", "CRUSH"
- Don't translate brand names: "Nexara", "Proxmox", "Ceph", "Proxmox Backup Server"
- Don't add or remove keys — keep the same structure as English

### Pluralization

i18next supports pluralization with `_one` / `_other` suffixes:

```json
{
  "recoveryCodesRemaining_one": "{{count}} recovery code remaining",
  "recoveryCodesRemaining_other": "{{count}} recovery codes remaining"
}
```

Some languages need additional plural forms. See [i18next pluralization docs](https://www.i18next.com/translation-function/plurals) for your language's rules.

## Testing Your Translations

1. Start the dev server: `cd frontend && npm run dev`
2. Go to Settings > Appearance > Language
3. Select your language
4. Navigate through the app to verify all strings are translated
5. Check for text overflow, especially in buttons and table headers

## Submitting

1. Create a branch: `git checkout -b feat/i18n-<lang-code>`
2. Commit your translations: `git commit -m "feat: add <language> translations"`
3. Open a pull request with a summary of what was translated
