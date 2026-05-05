package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// AttachmentRef is a lightweight reference to a binary attachment stored in S3.
// NATS messages carry AttachmentRef, never raw bytes (per ADR-0053).
type AttachmentRef struct {
	ID        string `json:"id"`
	Filename  string `json:"filename"`
	MimeType  string `json:"mimeType"`
	SizeBytes int64  `json:"sizeBytes"`
}

// attachmentDownloadResponse is the CP response to attachments.download request.
type attachmentDownloadResponse struct {
	DownloadURL string `json:"downloadUrl"`
}

// attachmentUploadResponse is the CP response to attachments.upload request.
type attachmentUploadResponse struct {
	ID        string `json:"id"`
	UploadURL string `json:"uploadUrl"`
}

// downloadAttachment downloads a binary attachment from S3 using a pre-signed URL.
// It requests the URL from the control-plane via NATS request/reply, then downloads
// directly from S3.
// Per ADR-0053 / spec-session-agent.md §Attachment Download.
func (a *Agent) downloadAttachment(ctx context.Context, attachmentID string) ([]byte, error) {
	downloadSubject := "mclaude.users." + string(a.userSlug) +
		".hosts." + string(a.hostSlug) +
		".projects." + string(a.projectSlug) + ".attachments.download"

	reqData, _ := json.Marshal(map[string]string{"id": attachmentID})
	reply, err := a.nc.RequestWithContext(ctx, downloadSubject, reqData)
	if err != nil {
		return nil, fmt.Errorf("attachments.download request: %w", err)
	}

	var dlResp attachmentDownloadResponse
	if err := json.Unmarshal(reply.Data, &dlResp); err != nil {
		return nil, fmt.Errorf("attachments.download response parse: %w", err)
	}
	if dlResp.DownloadURL == "" {
		return nil, fmt.Errorf("attachments.download returned empty URL")
	}

	data, err := a.fetchFromS3(ctx, dlResp.DownloadURL)
	if err != nil {
		// Pre-signed URL may have expired — request a new one and retry once.
		a.log.Warn().Err(err).Str("attachmentId", attachmentID).
			Msg("attachment S3 download failed; requesting new URL")
		reply2, retryErr := a.nc.RequestWithContext(ctx, downloadSubject, reqData)
		if retryErr != nil {
			return nil, fmt.Errorf("attachment.download retry request: %w", retryErr)
		}
		var dlResp2 attachmentDownloadResponse
		if parseErr := json.Unmarshal(reply2.Data, &dlResp2); parseErr != nil || dlResp2.DownloadURL == "" {
			return nil, fmt.Errorf("attachment.download retry returned empty URL")
		}
		data, err = a.fetchFromS3(ctx, dlResp2.DownloadURL)
		if err != nil {
			return nil, fmt.Errorf("attachment S3 download retry failed: %w", err)
		}
	}
	return data, nil
}

// uploadAttachment uploads binary data to S3 via a pre-signed URL obtained from CP.
// Returns the AttachmentRef for inclusion in session event content blocks.
// Per ADR-0053 / spec-session-agent.md §Attachment Upload (Agent-Generated).
func (a *Agent) uploadAttachment(ctx context.Context, data []byte, filename, mimeType string) (*AttachmentRef, error) {
	// Step 1: request pre-signed upload URL from CP.
	uploadSubject := "mclaude.users." + string(a.userSlug) +
		".hosts." + string(a.hostSlug) +
		".projects." + string(a.projectSlug) + ".attachments.upload"

	reqData, _ := json.Marshal(map[string]interface{}{
		"filename":  filename,
		"mimeType":  mimeType,
		"sizeBytes": int64(len(data)),
	})
	reply, err := a.nc.RequestWithContext(ctx, uploadSubject, reqData)
	if err != nil {
		return nil, fmt.Errorf("attachments.upload request: %w", err)
	}

	var upResp attachmentUploadResponse
	if err := json.Unmarshal(reply.Data, &upResp); err != nil {
		return nil, fmt.Errorf("attachments.upload response parse: %w", err)
	}
	if upResp.UploadURL == "" {
		return nil, fmt.Errorf("attachments.upload returned empty URL")
	}

	// Step 2: upload directly to S3.
	if err := a.putToS3(ctx, upResp.UploadURL, data, mimeType); err != nil {
		// Retry with a new URL.
		a.log.Warn().Err(err).Msg("attachment upload failed; retrying with new URL")
		reply2, retryErr := a.nc.RequestWithContext(ctx, uploadSubject, reqData)
		if retryErr != nil {
			return nil, fmt.Errorf("attachment upload retry request: %w", retryErr)
		}
		var upResp2 attachmentUploadResponse
		if parseErr := json.Unmarshal(reply2.Data, &upResp2); parseErr != nil || upResp2.UploadURL == "" {
			return nil, fmt.Errorf("attachment upload retry returned empty URL")
		}
		if err2 := a.putToS3(ctx, upResp2.UploadURL, data, mimeType); err2 != nil {
			return nil, fmt.Errorf("attachment S3 upload retry failed: %w", err2)
		}
		upResp = upResp2
	}

	// Step 3: confirm upload via NATS request/reply.
	confirmSubject := "mclaude.users." + string(a.userSlug) +
		".hosts." + string(a.hostSlug) +
		".projects." + string(a.projectSlug) + ".attachments.confirm"
	confirmData, _ := json.Marshal(map[string]string{"id": upResp.ID})
	if _, confirmErr := a.nc.RequestWithContext(ctx, confirmSubject, confirmData); confirmErr != nil {
		// Non-fatal: CP may GC unconfirmed entries, but the upload succeeded.
		a.log.Warn().Err(confirmErr).Str("attachmentId", upResp.ID).
			Msg("attachment confirm request failed (non-fatal)")
	}

	return &AttachmentRef{
		ID:        upResp.ID,
		Filename:  filename,
		MimeType:  mimeType,
		SizeBytes: int64(len(data)),
	}, nil
}

// fetchFromS3 downloads bytes from a pre-signed S3 URL.
func (a *Agent) fetchFromS3(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build GET request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("S3 GET returned %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// putToS3 uploads bytes to a pre-signed S3 URL.
func (a *Agent) putToS3(ctx context.Context, url string, data []byte, mimeType string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("build PUT request: %w", err)
	}
	req.ContentLength = int64(len(data))
	if mimeType != "" {
		req.Header.Set("Content-Type", mimeType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("PUT: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("S3 PUT returned %d", resp.StatusCode)
	}
	return nil
}

// processInputAttachments resolves all AttachmentRef blocks in a user input message.
// For each AttachmentRef, downloads the file from S3 and writes it to a temp file.
// Returns the temp file paths (caller is responsible for cleanup).
// Called by handleInput when the message contains AttachmentRef content blocks.
func (a *Agent) processInputAttachments(ctx context.Context, messageData []byte) (map[string]string, error) {
	// Parse message to find AttachmentRef blocks.
	var msg struct {
		Attachments []AttachmentRef `json:"attachments"`
	}
	if err := json.Unmarshal(messageData, &msg); err != nil || len(msg.Attachments) == 0 {
		return nil, nil
	}

	tmpPaths := make(map[string]string, len(msg.Attachments))
	for _, ref := range msg.Attachments {
		data, err := a.downloadAttachment(ctx, ref.ID)
		if err != nil {
			a.log.Warn().Err(err).Str("attachmentId", ref.ID).
				Msg("failed to download attachment (continuing without it)")
			continue
		}

		// Write to a temp file with a recognizable name.
		tmpFile, err := os.CreateTemp("", "mclaude-attach-*-"+ref.Filename)
		if err != nil {
			a.log.Warn().Err(err).Str("attachmentId", ref.ID).
				Msg("failed to create temp file for attachment (continuing without it)")
			continue
		}
		if _, err := tmpFile.Write(data); err != nil {
			tmpFile.Close()
			os.Remove(tmpFile.Name())
			continue
		}
		tmpFile.Close()
		tmpPaths[ref.ID] = tmpFile.Name()
	}
	return tmpPaths, nil
}

// attachmentTimeoutCtx creates a context with a default timeout for S3 operations.
func attachmentTimeoutCtx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 30*time.Second)
}
