/**
 * Attachment client — handles S3 pre-signed URL upload/download flow.
 *
 * Upload flow (ADR-0053):
 *   1. POST /api/attachments/upload-url {filename, mimeType, sizeBytes, projectSlug, hostSlug}
 *      → {id, uploadUrl}
 *   2. PUT uploadUrl with file bytes (direct to S3)
 *   3. POST /api/attachments/{id}/confirm
 *
 * Download flow:
 *   GET /api/attachments/{id} → {id, filename, mimeType, sizeBytes, downloadUrl}
 */

/** Maximum file size: 50MB (enforced client-side; CP also enforces). */
export const MAX_ATTACHMENT_BYTES = 50 * 1024 * 1024

export interface AttachmentRef {
  id: string
  filename: string
  mimeType: string
  sizeBytes: number
}

export interface AttachmentMeta {
  id: string
  filename: string
  mimeType: string
  sizeBytes: number
  downloadUrl: string
}

export class AttachmentClient {
  constructor(private readonly baseUrl: string, private readonly getJwt: () => string | null) {}

  private get _authHeaders(): Record<string, string> {
    const jwt = this.getJwt()
    return jwt ? { Authorization: `Bearer ${jwt}` } : {}
  }

  /**
   * Upload a file via S3 pre-signed URL.
   * Returns an AttachmentRef to embed in the outgoing NATS message.
   * Throws if the file exceeds MAX_ATTACHMENT_BYTES.
   */
  async upload(
    file: File,
    projectSlug: string,
    hostSlug: string,
  ): Promise<AttachmentRef> {
    if (file.size > MAX_ATTACHMENT_BYTES) {
      throw new Error(
        `File too large (${(file.size / 1024 / 1024).toFixed(1)} MB). Maximum is 50 MB.`,
      )
    }

    // Step 1: request pre-signed upload URL from CP
    const urlRes = await fetch(`${this.baseUrl}/api/attachments/upload-url`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...this._authHeaders,
      },
      body: JSON.stringify({
        filename: file.name,
        mimeType: file.type || 'application/octet-stream',
        sizeBytes: file.size,
        projectSlug,
        hostSlug,
      }),
    })
    if (!urlRes.ok) {
      const body = await urlRes.text().catch(() => '')
      throw new Error(body || `upload-url request failed: ${urlRes.status}`)
    }
    const { id, uploadUrl } = (await urlRes.json()) as { id: string; uploadUrl: string }

    // Step 2: upload directly to S3 using the pre-signed URL
    const putRes = await fetch(uploadUrl, {
      method: 'PUT',
      headers: { 'Content-Type': file.type || 'application/octet-stream' },
      body: file,
    })
    if (!putRes.ok) {
      throw new Error(`S3 upload failed: ${putRes.status}`)
    }

    // Step 3: confirm the upload with CP
    const confirmRes = await fetch(`${this.baseUrl}/api/attachments/${id}/confirm`, {
      method: 'POST',
      headers: { ...this._authHeaders },
    })
    if (!confirmRes.ok) {
      throw new Error(`confirm failed: ${confirmRes.status}`)
    }

    return {
      id,
      filename: file.name,
      mimeType: file.type || 'application/octet-stream',
      sizeBytes: file.size,
    }
  }

  /**
   * Resolve a pre-signed download URL for an attachment.
   * Returns the full attachment metadata including a time-limited downloadUrl.
   */
  async getDownloadMeta(id: string): Promise<AttachmentMeta> {
    const res = await fetch(`${this.baseUrl}/api/attachments/${id}`, {
      headers: this._authHeaders,
    })
    if (!res.ok) {
      throw new Error(`GET /api/attachments/${id} failed: ${res.status}`)
    }
    return res.json() as Promise<AttachmentMeta>
  }
}
