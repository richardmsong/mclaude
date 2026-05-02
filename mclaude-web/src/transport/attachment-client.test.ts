import { describe, it, expect, vi, beforeEach } from 'vitest'
import { AttachmentClient, MAX_ATTACHMENT_BYTES } from './attachment-client'

const BASE_URL = 'https://api.test'
const JWT = 'test-jwt'

function makeClient(): AttachmentClient {
  return new AttachmentClient(BASE_URL, () => JWT)
}

function makeFile(name: string, type: string, sizeBytes: number): File {
  const content = new Uint8Array(sizeBytes)
  return new File([content], name, { type })
}

describe('AttachmentClient', () => {
  beforeEach(() => {
    vi.resetAllMocks()
  })

  describe('MAX_ATTACHMENT_BYTES', () => {
    it('is 50MB', () => {
      expect(MAX_ATTACHMENT_BYTES).toBe(50 * 1024 * 1024)
    })
  })

  describe('upload', () => {
    it('throws if file exceeds 50MB', async () => {
      const client = makeClient()
      const bigFile = makeFile('big.bin', 'application/octet-stream', MAX_ATTACHMENT_BYTES + 1)
      await expect(client.upload(bigFile, 'project-slug', 'local')).rejects.toThrow('too large')
    })

    it('calls upload-url, PUT to S3, then confirm', async () => {
      const client = makeClient()
      const file = makeFile('photo.png', 'image/png', 1024)

      const uploadUrl = 'https://s3.test/bucket/key?X-Amz-Signature=abc'
      const attachmentId = 'att-001'

      // Mock: POST /api/attachments/upload-url → {id, uploadUrl}
      const fetchMock = vi.fn()
        .mockResolvedValueOnce({
          ok: true,
          json: () => Promise.resolve({ id: attachmentId, uploadUrl }),
          text: () => Promise.resolve(''),
        })
        // Mock: PUT to S3
        .mockResolvedValueOnce({ ok: true })
        // Mock: POST /api/attachments/{id}/confirm
        .mockResolvedValueOnce({ ok: true })

      globalThis.fetch = fetchMock as unknown as typeof fetch

      const ref = await client.upload(file, 'my-project', 'local')

      // Check AttachmentRef shape
      expect(ref.id).toBe(attachmentId)
      expect(ref.filename).toBe('photo.png')
      expect(ref.mimeType).toBe('image/png')
      expect(ref.sizeBytes).toBe(1024)

      // Step 1: POST upload-url
      expect(fetchMock).toHaveBeenNthCalledWith(
        1,
        `${BASE_URL}/api/attachments/upload-url`,
        expect.objectContaining({
          method: 'POST',
          headers: expect.objectContaining({
            Authorization: `Bearer ${JWT}`,
            'Content-Type': 'application/json',
          }),
          body: JSON.stringify({
            filename: 'photo.png',
            mimeType: 'image/png',
            sizeBytes: 1024,
            projectSlug: 'my-project',
            hostSlug: 'local',
          }),
        }),
      )

      // Step 2: PUT to S3
      expect(fetchMock).toHaveBeenNthCalledWith(
        2,
        uploadUrl,
        expect.objectContaining({ method: 'PUT' }),
      )

      // Step 3: POST confirm
      expect(fetchMock).toHaveBeenNthCalledWith(
        3,
        `${BASE_URL}/api/attachments/${attachmentId}/confirm`,
        expect.objectContaining({ method: 'POST' }),
      )
    })

    it('throws on failed upload-url request', async () => {
      const client = makeClient()
      const file = makeFile('doc.pdf', 'application/pdf', 100)

      globalThis.fetch = vi.fn().mockResolvedValueOnce({
        ok: false,
        status: 400,
        text: () => Promise.resolve('bad request'),
      }) as unknown as typeof fetch

      await expect(client.upload(file, 'p', 'local')).rejects.toThrow('bad request')
    })

    it('throws on failed S3 PUT', async () => {
      const client = makeClient()
      const file = makeFile('img.jpg', 'image/jpeg', 512)

      globalThis.fetch = vi.fn()
        .mockResolvedValueOnce({
          ok: true,
          json: () => Promise.resolve({ id: 'att-002', uploadUrl: 'https://s3.test/x' }),
          text: () => Promise.resolve(''),
        })
        .mockResolvedValueOnce({ ok: false, status: 403 }) as unknown as typeof fetch

      await expect(client.upload(file, 'p', 'local')).rejects.toThrow('S3 upload failed')
    })

    it('throws on failed confirm', async () => {
      const client = makeClient()
      const file = makeFile('file.txt', 'text/plain', 64)

      globalThis.fetch = vi.fn()
        .mockResolvedValueOnce({
          ok: true,
          json: () => Promise.resolve({ id: 'att-003', uploadUrl: 'https://s3.test/y' }),
          text: () => Promise.resolve(''),
        })
        .mockResolvedValueOnce({ ok: true })
        .mockResolvedValueOnce({ ok: false, status: 500 }) as unknown as typeof fetch

      await expect(client.upload(file, 'p', 'local')).rejects.toThrow('confirm failed')
    })

    it('uses application/octet-stream as fallback mimeType', async () => {
      const client = makeClient()
      // File with empty type string
      const file = new File([new Uint8Array(10)], 'data.bin', { type: '' })

      globalThis.fetch = vi.fn()
        .mockResolvedValueOnce({
          ok: true,
          json: () => Promise.resolve({ id: 'att-004', uploadUrl: 'https://s3.test/z' }),
          text: () => Promise.resolve(''),
        })
        .mockResolvedValueOnce({ ok: true })
        .mockResolvedValueOnce({ ok: true }) as unknown as typeof fetch

      const ref = await client.upload(file, 'p', 'local')
      expect(ref.mimeType).toBe('application/octet-stream')
    })
  })

  describe('getDownloadMeta', () => {
    it('returns download metadata with downloadUrl', async () => {
      const client = makeClient()
      const meta = {
        id: 'att-001',
        filename: 'photo.png',
        mimeType: 'image/png',
        sizeBytes: 1024,
        downloadUrl: 'https://s3.test/bucket/photo.png?X-Amz-Signature=xyz',
      }

      globalThis.fetch = vi.fn().mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve(meta),
      }) as unknown as typeof fetch

      const result = await client.getDownloadMeta('att-001')
      expect(result).toEqual(meta)

      expect(globalThis.fetch).toHaveBeenCalledWith(
        `${BASE_URL}/api/attachments/att-001`,
        expect.objectContaining({
          headers: expect.objectContaining({ Authorization: `Bearer ${JWT}` }),
        }),
      )
    })

    it('throws on non-200 response', async () => {
      const client = makeClient()
      globalThis.fetch = vi.fn().mockResolvedValueOnce({
        ok: false,
        status: 404,
      }) as unknown as typeof fetch

      await expect(client.getDownloadMeta('att-999')).rejects.toThrow('404')
    })
  })
})
