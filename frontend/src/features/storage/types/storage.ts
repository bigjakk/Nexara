export interface StorageContentItem {
  volid: string;
  format: string;
  size: number;
  ctime: number;
  content: string;
  vmid?: number;
}

export interface UploadRequest {
  content: "iso" | "vztmpl";
  file: File;
}

export interface StorageActionResponse {
  upid: string;
  status: string;
}

/** Body for POST /api/v1/clusters/:cid/storage/:sid/oci-pull */
export interface OCIPullRequest {
  reference: string;
  file_name?: string;
}

/** Body for POST /api/v1/clusters/:cid/storage/:sid/download-url */
export interface DownloadURLRequest {
  url: string;
  content: "iso" | "vztmpl" | "import";
  filename: string;
  checksum?: string;
  checksum_algorithm?:
    | "md5"
    | "sha1"
    | "sha224"
    | "sha256"
    | "sha384"
    | "sha512";
  decompression_algorithm?: "gz" | "lzo" | "zst" | "bz2";
  verify_certificates?: boolean;
}

/** Body for POST /api/v1/clusters/:cid/storage/:sid/appliances */
export interface DownloadApplianceRequest {
  template: string;
}

/** Appliance entry from GET /api/v1/clusters/:cid/appliances */
export interface ApplianceTemplate {
  template: string;
  os: string;
  type: string;
  version: string;
  section: string;
  package: string;
  description: string;
  headline: string;
  info_page?: string;
  source?: string;
  location?: string;
  manage_url?: string;
  sha512sum?: string;
  architecture?: string;
}

export interface DiskResizeRequest {
  disk: string;
  size: string;
}

export interface DiskMoveRequest {
  disk: string;
  storage: string;
  /** Target image format; omit to let the target storage decide. */
  format?: string;
  delete: boolean;
  bwlimit_kib?: number;
}

/** Proxmox storage type identifiers */
export type StorageType =
  | "dir"
  | "nfs"
  | "cifs"
  | "lvm"
  | "lvmthin"
  | "zfspool"
  | "iscsi"
  | "iscsidirect"
  | "rbd"
  | "cephfs"
  | "glusterfs"
  | "btrfs"
  | "pbs";

/** Content types that can be stored */
export type StorageContentType =
  | "images"
  | "rootdir"
  | "iso"
  | "vztmpl"
  | "backup"
  | "snippets";

/** Create storage request body */
export interface CreateStorageRequest {
  storage: string;
  type: StorageType;
  params: Record<string, string>;
}

/** Update storage request body */
export interface UpdateStorageRequest {
  params: Record<string, string>;
  delete?: string;
}

/** Full storage config from Proxmox (GET /storage/{id}/config) */
export interface StorageConfigResponse {
  storage: string;
  type: string;
  content?: string;
  nodes?: string;
  disable?: number;
  shared?: number;
  digest?: string;
  path?: string;
  mkdir?: number;
  is_mountpoint?: string;
  server?: string;
  export?: string;
  options?: string;
  share?: string;
  username?: string;
  domain?: string;
  smbversion?: string;
  password?: string;
  vgname?: string;
  base?: string;
  saferemove?: number;
  thinpool?: string;
  pool?: string;
  blocksize?: string;
  sparse?: number;
  portal?: string;
  target?: string;
  monhost?: string;
  krbd?: number;
  fuse?: number;
  subdir?: string;
  "fs-name"?: string;
  keyring?: string;
  namespace?: string;
  server2?: string;
  volume?: string;
  transport?: string;
  datastore?: string;
  fingerprint?: string;
  "encryption-key"?: string;
  preallocation?: string;
  format?: string;
  maxfiles?: number;
  "prune-backups"?: string;
}

/** Metadata describing a field in the storage form */
export interface StorageFieldDef {
  key: string;
  label: string;
  required?: boolean;
  type?: "text" | "select" | "number" | "password" | "checkbox";
  options?: { value: string; label: string }[];
  placeholder?: string;
  help?: string;
  /**
   * Renders the field as a discovery-backed combobox instead of a plain input.
   * "iscsi" scans the portal named by `scanFrom` for advertised target IQNs,
   * the way the PVE GUI fills its Target dropdown. Manual entry stays available
   * for portals that refuse discovery.
   */
  scan?: "iscsi";
  /** Key of the field whose value drives `scan` (e.g. the portal address). */
  scanFrom?: string;
  /**
   * Set at creation only. Proxmox marks the backend-identifying options of some
   * plugins `fixed => 1` and rejects a PUT that carries them, so the edit form
   * shows them read-only rather than offering an edit that cannot succeed.
   */
  fixed?: boolean;
}

/** A target IQN advertised by an iSCSI portal, from GET /clusters/:id/scan/iscsi */
export interface ISCSITarget {
  target: string;
  portal: string;
}

/** Which content types each storage type supports */
export const STORAGE_TYPE_CONTENT: Record<StorageType, StorageContentType[]> = {
  dir: ["images", "rootdir", "iso", "vztmpl", "backup", "snippets"],
  btrfs: ["images", "rootdir", "iso", "vztmpl", "backup", "snippets"],
  nfs: ["images", "rootdir", "iso", "vztmpl", "backup", "snippets"],
  cifs: ["images", "rootdir", "iso", "vztmpl", "backup", "snippets"],
  glusterfs: ["images", "rootdir", "iso", "vztmpl", "backup", "snippets"],
  lvm: ["images", "rootdir"],
  lvmthin: ["images", "rootdir"],
  zfspool: ["images", "rootdir"],
  iscsi: ["images"],
  iscsidirect: ["images"],
  rbd: ["images", "rootdir"],
  cephfs: ["images", "rootdir", "iso", "vztmpl", "backup", "snippets"],
  pbs: ["backup"],
};

/** Labels for storage types */
export const STORAGE_TYPE_LABELS: Record<StorageType, string> = {
  dir: "Directory",
  btrfs: "BTRFS",
  nfs: "NFS",
  cifs: "CIFS/SMB",
  glusterfs: "GlusterFS",
  lvm: "LVM",
  lvmthin: "LVM-Thin",
  zfspool: "ZFS",
  iscsi: "iSCSI",
  iscsidirect: "iSCSI Direct",
  rbd: "RBD (Ceph)",
  cephfs: "CephFS",
  pbs: "Proxmox Backup Server",
};

/**
 * Storage types whose `images` volumes can be allocated in a chosen format.
 *
 * These are the file-based plugins (raw | qcow2 | vmdk). Block-backed plugins
 * (LVM, LVM-thin, ZFS, RBD, iSCSI) only ever hold raw, and BTRFS is limited to
 * raw/subvol, so a format choice is meaningless — and rejected — for those.
 */
const FORMAT_CHOICE_STORAGE_TYPES = new Set<string>([
  "dir",
  "nfs",
  "cifs",
  "glusterfs",
]);

/** Image formats offered when the target storage is file-based. */
export const DISK_IMAGE_FORMATS = [
  { value: "qcow2", label: "QEMU image format (qcow2)" },
  { value: "raw", label: "Raw disk image (raw)" },
  { value: "vmdk", label: "VMware image format (vmdk)" },
] as const;

/**
 * Whether a disk moved onto this storage type can have its format chosen.
 * Mirrors how Proxmox greys out the Format field in its Move disk dialog.
 */
export function storageSupportsFormatChoice(type: string | undefined): boolean {
  return type !== undefined && FORMAT_CHOICE_STORAGE_TYPES.has(type);
}

/** Type-specific fields for each storage type */
export const STORAGE_TYPE_FIELDS: Record<StorageType, StorageFieldDef[]> = {
  dir: [
    {
      key: "path",
      label: "Directory Path",
      required: true,
      placeholder: "/mnt/storage",
    },
    { key: "mkdir", label: "Create Directory", type: "checkbox" },
    { key: "is_mountpoint", label: "Is Mountpoint", type: "checkbox" },
    {
      key: "preallocation",
      label: "Preallocation",
      type: "select",
      options: [
        { value: "", label: "Default" },
        { value: "off", label: "Off" },
        { value: "metadata", label: "Metadata" },
        { value: "falloc", label: "Falloc" },
        { value: "full", label: "Full" },
      ],
    },
  ],
  btrfs: [
    {
      key: "path",
      label: "Directory Path",
      required: true,
      placeholder: "/mnt/btrfs",
    },
    { key: "mkdir", label: "Create Directory", type: "checkbox" },
  ],
  nfs: [
    {
      key: "server",
      label: "Server",
      required: true,
      placeholder: "192.168.1.100",
    },
    {
      key: "export",
      label: "Export Path",
      required: true,
      placeholder: "/export/share",
    },
    { key: "options", label: "NFS Options", placeholder: "vers=4.2" },
    {
      key: "preallocation",
      label: "Preallocation",
      type: "select",
      options: [
        { value: "", label: "Default" },
        { value: "off", label: "Off" },
        { value: "metadata", label: "Metadata" },
        { value: "falloc", label: "Falloc" },
        { value: "full", label: "Full" },
      ],
    },
  ],
  cifs: [
    {
      key: "server",
      label: "Server",
      required: true,
      placeholder: "192.168.1.100",
    },
    {
      key: "share",
      label: "Share Name",
      required: true,
      placeholder: "backups",
    },
    { key: "username", label: "Username", placeholder: "admin" },
    { key: "password", label: "Password", type: "password" },
    { key: "domain", label: "Domain" },
    {
      key: "smbversion",
      label: "SMB Version",
      type: "select",
      options: [
        { value: "", label: "Default" },
        { value: "2.0", label: "2.0" },
        { value: "2.1", label: "2.1" },
        { value: "3", label: "3.0" },
        { value: "3.0", label: "3.0 (strict)" },
        { value: "3.11", label: "3.11" },
      ],
    },
  ],
  lvm: [
    {
      key: "vgname",
      label: "Volume Group",
      required: true,
      placeholder: "pve",
    },
    { key: "base", label: "Base Volume" },
    { key: "saferemove", label: "Safe Remove", type: "checkbox" },
  ],
  lvmthin: [
    {
      key: "vgname",
      label: "Volume Group",
      required: true,
      placeholder: "pve",
    },
    {
      key: "thinpool",
      label: "Thin Pool",
      required: true,
      placeholder: "data",
    },
  ],
  zfspool: [
    {
      key: "pool",
      label: "ZFS Pool",
      required: true,
      placeholder: "rpool/data",
    },
    { key: "blocksize", label: "Block Size", placeholder: "8k" },
    { key: "sparse", label: "Sparse Volumes", type: "checkbox" },
  ],
  // Both iSCSI plugins declare portal and target `fixed => 1` upstream — they
  // identify the backend, so Proxmox only accepts them at creation.
  iscsi: [
    {
      key: "portal",
      label: "Portal (IP/Host)",
      required: true,
      placeholder: "192.168.1.100",
      fixed: true,
    },
    {
      key: "target",
      label: "Target IQN",
      required: true,
      placeholder: "iqn.2024-01.com.example:target",
      scan: "iscsi",
      scanFrom: "portal",
      fixed: true,
    },
  ],
  iscsidirect: [
    {
      key: "portal",
      label: "Portal (IP/Host)",
      required: true,
      placeholder: "192.168.1.100",
      fixed: true,
    },
    {
      key: "target",
      label: "Target IQN",
      required: true,
      placeholder: "iqn.2024-01.com.example:target",
      scan: "iscsi",
      scanFrom: "portal",
      fixed: true,
    },
  ],
  rbd: [
    {
      key: "monhost",
      label: "Monitor Hosts",
      placeholder: "10.0.0.1,10.0.0.2",
    },
    { key: "pool", label: "Ceph Pool", placeholder: "rbd" },
    { key: "username", label: "Ceph User", placeholder: "admin" },
    { key: "krbd", label: "Use Kernel RBD", type: "checkbox" },
    { key: "keyring", label: "Keyring Path" },
    { key: "namespace", label: "Namespace" },
  ],
  cephfs: [
    {
      key: "monhost",
      label: "Monitor Hosts",
      placeholder: "10.0.0.1,10.0.0.2",
    },
    { key: "path", label: "Mount Path", placeholder: "/" },
    { key: "username", label: "Ceph User", placeholder: "admin" },
    { key: "fuse", label: "Use FUSE", type: "checkbox" },
    { key: "subdir", label: "Subdirectory" },
    { key: "fs-name", label: "FS Name" },
  ],
  glusterfs: [
    {
      key: "server",
      label: "Primary Server",
      required: true,
      placeholder: "192.168.1.100",
    },
    { key: "server2", label: "Backup Server", placeholder: "192.168.1.101" },
    { key: "volume", label: "Volume Name", required: true, placeholder: "gv0" },
    {
      key: "transport",
      label: "Transport",
      type: "select",
      options: [
        { value: "", label: "Default (tcp)" },
        { value: "tcp", label: "TCP" },
        { value: "rdma", label: "RDMA" },
        { value: "unix", label: "Unix" },
      ],
    },
  ],
  pbs: [
    {
      key: "server",
      label: "Server",
      required: true,
      placeholder: "pbs.example.com",
    },
    {
      key: "datastore",
      label: "Datastore",
      required: true,
      placeholder: "main",
    },
    {
      key: "username",
      label: "Username",
      required: true,
      placeholder: "backup@pbs!token",
    },
    {
      key: "password",
      label: "Password / API Token",
      required: true,
      type: "password",
    },
    {
      key: "fingerprint",
      label: "TLS Fingerprint",
      placeholder: "AA:BB:CC:...",
    },
    { key: "encryption-key", label: "Encryption Key" },
  ],
};
