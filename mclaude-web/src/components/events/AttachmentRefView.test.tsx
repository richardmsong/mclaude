// @vitest-environment jsdom
import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { AttachmentRefView } from './AttachmentRefView'
import type { AttachmentRefBlock } from '@/types'

function makeBlock(overrides: Partial<AttachmentRefBlock> = {}): AttachmentRefBlock {
  return {
    type: 'attachment_ref',
    id: 'att-001',
    filename: 'photo.png',
    mimeType: 'image/png',
    sizeBytes: 245000,
    ...overrides,
  }
}

describe('AttachmentRefView', () => {
  it('shows loading state initially', () => {
    const fetchUrl = vi.fn(() => new Promise<string>(() => {})) // never resolves
    render(
      <AttachmentRefView
        block={makeBlock()}
        onFetchDownloadUrl={fetchUrl}
      />,
    )
    expect(screen.getByTestId('attachment-loading')).toBeTruthy()
  })

  it('renders an image inline when mimeType starts with image/', async () => {
    const downloadUrl = 'https://s3.test/bucket/photo.png?sig=abc'
    const fetchUrl = vi.fn().mockResolvedValue(downloadUrl)
    render(
      <AttachmentRefView
        block={makeBlock({ mimeType: 'image/png', filename: 'photo.png', sizeBytes: 245000 })}
        onFetchDownloadUrl={fetchUrl}
      />,
    )
    await waitFor(() => {
      expect(screen.getByTestId('attachment-image')).toBeTruthy()
    })
    const img = screen.getByAltText('photo.png') as HTMLImageElement
    expect(img.src).toBe(downloadUrl)
  })

  it('renders a download link for non-image files', async () => {
    const downloadUrl = 'https://s3.test/bucket/report.pdf?sig=def'
    const fetchUrl = vi.fn().mockResolvedValue(downloadUrl)
    render(
      <AttachmentRefView
        block={makeBlock({ mimeType: 'application/pdf', filename: 'report.pdf', sizeBytes: 1024 * 50 })}
        onFetchDownloadUrl={fetchUrl}
      />,
    )
    await waitFor(() => {
      expect(screen.getByTestId('attachment-download')).toBeTruthy()
    })
    const link = screen.getByRole('link') as HTMLAnchorElement
    expect(link.href).toBe(downloadUrl)
    expect(link.textContent).toBe('report.pdf')
    expect(link.getAttribute('download')).toBe('report.pdf')
  })

  it('shows error state when fetch fails', async () => {
    const fetchUrl = vi.fn().mockRejectedValue(new Error('network error'))
    render(
      <AttachmentRefView
        block={makeBlock()}
        onFetchDownloadUrl={fetchUrl}
      />,
    )
    await waitFor(() => {
      expect(screen.getByTestId('attachment-error')).toBeTruthy()
    })
    expect(screen.getByTestId('attachment-error').textContent).toContain('photo.png')
  })

  it('calls onFetchDownloadUrl with the attachment id', async () => {
    const downloadUrl = 'https://s3.test/x'
    const fetchUrl = vi.fn().mockResolvedValue(downloadUrl)
    render(
      <AttachmentRefView
        block={makeBlock({ id: 'att-xyz' })}
        onFetchDownloadUrl={fetchUrl}
      />,
    )
    await waitFor(() => fetchUrl.mock.calls.length > 0)
    expect(fetchUrl).toHaveBeenCalledWith('att-xyz')
  })

  it('shows filename and size for non-image files', async () => {
    const fetchUrl = vi.fn().mockResolvedValue('https://s3.test/x')
    render(
      <AttachmentRefView
        block={makeBlock({ mimeType: 'text/plain', filename: 'notes.txt', sizeBytes: 2048 })}
        onFetchDownloadUrl={fetchUrl}
      />,
    )
    await waitFor(() => {
      expect(screen.getByTestId('attachment-download')).toBeTruthy()
    })
    expect(screen.getByText('notes.txt')).toBeTruthy()
    // 2048 bytes = 2.0 KB
    expect(screen.getByTestId('attachment-download').textContent).toContain('KB')
  })
})
