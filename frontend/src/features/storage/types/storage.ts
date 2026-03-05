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

export interface DiskResizeRequest {
  disk: string;
  size: string;
}

export interface DiskMoveRequest {
  disk: string;
  storage: string;
  delete: boolean;
}
