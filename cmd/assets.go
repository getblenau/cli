package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/text/unicode/norm"
)

// maxAssetBytes is the client-side hard cap on uploadable asset size (1 MiB).
const maxAssetBytes = 1 * 1024 * 1024

// imageUploadGuidance MUST match the server message; keep it in sync.
const imageUploadGuidance = `Images must be 1 MB or smaller. Optimize locally before uploading (do not read the file into context):
  ImageMagick:  magick input.png -resize '1600x1600>' -quality 80 output.webp
  cwebp:        cwebp -q 80 -resize 1600 0 input.png -o output.webp
  pngquant:     pngquant --quality=60-80 input.png
Install ImageMagick: macOS 'brew install imagemagick' | Windows 'winget install ImageMagick.ImageMagick' | Debian/Ubuntu 'apt install imagemagick'. A WebP at 1600px is usually 50-200 KB.`

// NewAssetsCmd builds `blenau assets ...`.
func NewAssetsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "assets",
		Short: "Manage document assets (images, files).",
	}
	c.PersistentFlags().Bool("json", false, "Emit JSON instead of human format.")
	c.AddCommand(newAssetsUploadCmd())
	return c
}

func newAssetsUploadCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "upload <file>",
		Short: "Upload an image/file to a document without reading it into context.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src := args[0]
			doc, _ := cmd.Flags().GetString("doc")
			alt, _ := cmd.Flags().GetString("alt")
			name, _ := cmd.Flags().GetString("name")
			insertAt, _ := cmd.Flags().GetString("insert-at")
			position, _ := cmd.Flags().GetString("position")
			compress, _ := cmd.Flags().GetBool("compress")
			if doc == "" {
				return fmt.Errorf("--doc is required")
			}

			info, err := os.Stat(src)
			if err != nil {
				return fmt.Errorf("stat file: %w", err)
			}
			if info.IsDir() {
				return fmt.Errorf("%s is a directory, not a file", src)
			}

			// Resolve the filename to store under.
			filename := name
			if filename == "" {
				filename = filepath.Base(src)
			}

			uploadPath := src
			var cleanup func()
			if info.Size() > maxAssetBytes {
				if !compress {
					return fmt.Errorf("file is %d bytes, over the %d byte limit.\n%s", info.Size(), maxAssetBytes, imageUploadGuidance)
				}
				tmp, err := compressImage(src)
				if err != nil {
					return err
				}
				cleanup = func() { _ = os.Remove(tmp) }
				ci, err := os.Stat(tmp)
				if err != nil {
					cleanup()
					return fmt.Errorf("stat compressed file: %w", err)
				}
				if ci.Size() > maxAssetBytes {
					cleanup()
					return fmt.Errorf("compressed file is still %d bytes, over the %d byte limit.\n%s", ci.Size(), maxAssetBytes, imageUploadGuidance)
				}
				uploadPath = tmp
				// Store the compressed asset under a .webp name.
				if name == "" {
					base := filepath.Base(src)
					if ext := filepath.Ext(base); ext != "" {
						base = strings.TrimSuffix(base, ext)
					}
					filename = base + ".webp"
				}
			}
			if cleanup != nil {
				defer cleanup()
			}

			raw, status, err := uploadAsset(uploadPath, filename, doc, alt, insertAt, position)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, func(b []byte) error {
				var m map[string]interface{}
				if err := json.Unmarshal(b, &m); err != nil {
					cmd.OutOrStdout().Write(norm.NFC.Bytes(b))
					return nil
				}
				w := cmd.OutOrStdout()
				assetPath, _ := m["asset_path"].(string)
				sha, _ := m["commit_sha"].(string)
				fmt.Fprintf(w, "committed %s  (%s)\n", norm.NFC.String(assetPath), norm.NFC.String(sha))
				if md, ok := m["markdown"].(string); ok && md != "" {
					fmt.Fprintf(w, "markdown: %s\n", norm.NFC.String(md))
				}
				if ins, ok := m["insert"].(map[string]interface{}); ok {
					if embedded, ok := ins["embedded"].(bool); ok && embedded {
						if h, ok := ins["heading"].(string); ok && h != "" {
							fmt.Fprintf(w, "embedded under %s\n", norm.NFC.String(h))
						} else {
							fmt.Fprintln(w, "embedded")
						}
					} else {
						reason, _ := ins["reason"].(string)
						if reason == "" {
							reason = "not embedded"
						}
						fmt.Fprintf(w, "not embedded: %s\n", norm.NFC.String(reason))
					}
				}
				return nil
			})
		},
	}
	c.Flags().String("doc", "", "Tenant-level doc path that will embed the asset (routes the commit). REQUIRED.")
	c.Flags().String("alt", "", "Markdown alt-text.")
	c.Flags().String("name", "", "Override the stored filename (default: base name of <file>).")
	c.Flags().String("insert-at", "", "Heading to embed the image under (e.g. \"## Setup\"). Empty = don't embed, just return markdown.")
	c.Flags().String("position", "after", "Where to embed relative to the heading: after|before|append|prepend.")
	c.Flags().Bool("compress", false, "If the file is over the limit, auto-compress with a local tool (magick/cwebp) before upload.")
	c.Flags().Bool("json", false, "Emit JSON instead of human format.")
	_ = c.MarkFlagRequired("doc")
	return c
}

// compressImage compresses src into a temp .webp using magick or cwebp.
// It returns the temp file path; the caller is responsible for removing it.
func compressImage(src string) (string, error) {
	tmp, err := os.CreateTemp("", "blenau-asset-*.webp")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()

	if magick, err := exec.LookPath("magick"); err == nil {
		cmd := exec.Command(magick, src, "-resize", "1600x1600>", "-quality", "80", tmpPath)
		if out, err := cmd.CombinedOutput(); err != nil {
			_ = os.Remove(tmpPath)
			return "", fmt.Errorf("magick compress failed: %w\n%s", err, strings.TrimSpace(string(out)))
		}
		return tmpPath, nil
	}
	if cwebp, err := exec.LookPath("cwebp"); err == nil {
		cmd := exec.Command(cwebp, "-q", "80", "-resize", "1600", "0", src, "-o", tmpPath)
		if out, err := cmd.CombinedOutput(); err != nil {
			_ = os.Remove(tmpPath)
			return "", fmt.Errorf("cwebp compress failed: %w\n%s", err, strings.TrimSpace(string(out)))
		}
		return tmpPath, nil
	}

	_ = os.Remove(tmpPath)
	return "", fmt.Errorf("no local image compressor found (need 'magick' or 'cwebp' on PATH).\n%s", imageUploadGuidance)
}

// uploadAsset POSTs a multipart/form-data request to /assets/upload-binary.
// Returns (response bytes, status code, error).
func uploadAsset(path, filename, doc, alt, insertHeading, insertPosition string) ([]byte, int, error) {
	apiURL, token, err := resolveAuth()
	if err != nil {
		return nil, 0, err
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return nil, 0, err
	}
	if _, err := io.Copy(fw, f); err != nil {
		return nil, 0, fmt.Errorf("read file: %w", err)
	}
	fields := map[string]string{
		"doc_path":        doc,
		"filename":        filename,
		"alt_text":        alt,
		"insert_heading":  insertHeading,
		"insert_position": insertPosition,
	}
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			return nil, 0, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, 0, err
	}

	req, err := http.NewRequest("POST", apiURL+"/assets/upload-binary", &buf)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("call %s: %w", req.URL, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return raw, resp.StatusCode, nil
}
