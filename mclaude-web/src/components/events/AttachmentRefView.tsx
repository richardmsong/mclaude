import { useEffect, useState } from 'react'
import type { AttachmentRefBlock } from '@/types'

interface AttachmentRefViewProps {
  block: AttachmentRefBlock
  /** Fetch the pre-signed download URL for this attachment. */
  onFetchDownloadUrl: (id: string) => Promise<string>
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}

/**
 * Renders an attachment_ref content block.
 *
 * ADR-0053 §Attachment Rendering:
 * - Images (image/*): inline <img> with the download URL as src
 * - Other files: download link with filename and file size
 *
 * The download URL is fetched from the CP (pre-signed, time-limited).
 */
export function AttachmentRefView({ block, onFetchDownloadUrl }: AttachmentRefViewProps) {
  const [downloadUrl, setDownloadUrl] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    setDownloadUrl(null)
    setError(null)
    onFetchDownloadUrl(block.id)
      .then(url => {
        if (!cancelled) setDownloadUrl(url)
      })
      .catch(() => {
        if (!cancelled) setError('Could not load attachment')
      })
    return () => {
      cancelled = true
    }
  }, [block.id, onFetchDownloadUrl])

  const isImage = block.mimeType.startsWith('image/')

  if (error) {
    return (
      <div
        data-testid="attachment-error"
        style={{
          margin: '4px 0',
          padding: '8px 12px',
          background: 'var(--surf)',
          border: '1px solid var(--border)',
          borderRadius: 8,
          color: 'var(--text2)',
          fontSize: 13,
        }}
      >
        {error}: {block.filename}
      </div>
    )
  }

  if (!downloadUrl) {
    // Loading state
    return (
      <div
        data-testid="attachment-loading"
        style={{
          margin: '4px 0',
          padding: '8px 12px',
          background: 'var(--surf)',
          border: '1px solid var(--border)',
          borderRadius: 8,
          color: 'var(--text2)',
          fontSize: 13,
        }}
      >
        Loading attachment…
      </div>
    )
  }

  if (isImage) {
    return (
      <div
        data-testid="attachment-image"
        style={{ margin: '4px 0', maxWidth: '100%' }}
      >
        <img
          src={downloadUrl}
          alt={block.filename}
          style={{
            maxWidth: '100%',
            maxHeight: 400,
            borderRadius: 8,
            display: 'block',
            border: '1px solid var(--border)',
          }}
        />
        <div style={{ color: 'var(--text3)', fontSize: 11, marginTop: 4 }}>
          {block.filename} · {formatBytes(block.sizeBytes)}
        </div>
      </div>
    )
  }

  // Non-image: download link
  return (
    <div
      data-testid="attachment-download"
      style={{
        margin: '4px 0',
        padding: '10px 14px',
        background: 'var(--surf)',
        border: '1px solid var(--border)',
        borderRadius: 8,
        display: 'flex',
        alignItems: 'center',
        gap: 10,
      }}
    >
      <span style={{ fontSize: 20 }}>📎</span>
      <div style={{ flex: 1, minWidth: 0 }}>
        <a
          href={downloadUrl}
          download={block.filename}
          target="_blank"
          rel="noreferrer"
          style={{
            color: 'var(--blue)',
            fontSize: 14,
            fontWeight: 500,
            textDecoration: 'none',
            display: 'block',
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
          }}
        >
          {block.filename}
        </a>
        <div style={{ color: 'var(--text3)', fontSize: 12, marginTop: 2 }}>
          {block.mimeType} · {formatBytes(block.sizeBytes)}
        </div>
      </div>
    </div>
  )
}
