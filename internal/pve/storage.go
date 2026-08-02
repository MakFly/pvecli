package pve

import (
	"context"
	"io"
	"net/url"
)

// Storage is one entry of GET /nodes/{node}/storage.
//
// Content is the field that explains most "why can't I put this file here"
// failures: it is an API constraint, not a naming convention. A .qcow2 will
// not go on a storage declared content=iso, and the error says so only if you
// already knew where to look.
type Storage struct {
	Storage string `json:"storage"`
	Type    string `json:"type"`
	Content string `json:"content"`

	Active  int `json:"active"`
	Enabled int `json:"enabled"`
	Shared  int `json:"shared"`

	Total int64 `json:"total,omitempty"`
	Used  int64 `json:"used,omitempty"`
	Avail int64 `json:"avail,omitempty"`
}

// ContentTypes splits the comma-separated content declaration.
//
// The splitting lives in storagedef.go, with the reason it cannot be a byte
// comparison: PVE does not guarantee the ORDER of that list. One shared helper,
// so the two families cannot drift apart on it.
func (s Storage) ContentTypes() []string { return splitContentTypes(s.Content) }

// Accepts reports whether this storage takes the given content type.
func (s Storage) Accepts(content string) bool { return contentAccepts(s.Content, content) }

// Storages lists the storages visible from a node, with their live usage.
//
// GET /nodes/{node}/storage
func (c *Client) Storages(ctx context.Context, node string) ([]Storage, error) {
	var out []Storage
	if err := c.get(ctx, epNodeStorage, []string{node}, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Volume is one entry of a storage's content listing.
type Volume struct {
	// VolID is the identifier every other endpoint expects, in the form
	// "storage:type/filename" — not a filesystem path.
	VolID   string `json:"volid"`
	Content string `json:"content"`
	Format  string `json:"format,omitempty"`
	Size    int64  `json:"size,omitempty"`
	Used    int64  `json:"used,omitempty"`
	VMID    int    `json:"vmid,omitempty"`
	CTime   int64  `json:"ctime,omitempty"`
	Notes   string `json:"notes,omitempty"`
}

// StorageContent lists what a storage holds, optionally filtered by content
// type (iso, vztmpl, backup, images, rootdir, snippets).
//
// GET /nodes/{node}/storage/{storage}/content
func (c *Client) StorageContent(ctx context.Context, node, storage, content string) ([]Volume, error) {
	var query url.Values
	if content != "" {
		query = url.Values{"content": {content}}
	}
	var out []Volume
	if err := c.get(ctx, epStorageContent, []string{node, storage}, query, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// DownloadOptions describes a file the NODE will fetch by itself.
//
// Schema read on the node with `pvesh usage /nodes/pve/storage/local/download-url -v`.
// Content is limited to iso, vztmpl and import — a VM disk cannot be dropped
// this way, which is the same content-type constraint as everywhere else.
type DownloadOptions struct {
	URL      string
	Content  string
	Filename string

	Checksum  string
	Algorithm string

	// Compression decompresses on arrival, for a .gz or .xz image.
	Compression string
	// VerifyCertificates defaults to on. Turning it off makes the node trust
	// whatever answers the URL.
	SkipTLSVerify bool
}

// Values renders the payload, so --dry-run and the request cannot drift apart.
func (o DownloadOptions) Values() url.Values {
	v := url.Values{
		"url":      {o.URL},
		"content":  {o.Content},
		"filename": {o.Filename},
	}
	if o.Checksum != "" {
		v.Set("checksum", o.Checksum)
		v.Set("checksum-algorithm", o.Algorithm)
	}
	if o.Compression != "" {
		v.Set("compression", o.Compression)
	}
	if o.SkipTLSVerify {
		v.Set("verify-certificates", "0")
	}
	return v
}

// DownloadURL asks the node to fetch a file into a storage, and returns the
// UPID of the task.
//
// The transfer happens NODE → INTERNET. Nothing travels through the machine
// running this CLI, which is why a 4 GB image over a slow home uplink is not
// the operator's problem — and why the progress to watch is the task log, not
// a local byte counter.
//
// POST /nodes/{node}/storage/{storage}/download-url
func (c *Client) DownloadURL(ctx context.Context, node, storage string, o DownloadOptions) (string, error) {
	var upid string
	if err := c.post(ctx, epStorageDownURL, []string{node, storage}, o.Values(), &upid); err != nil {
		return "", err
	}
	return upid, nil
}

// UploadOptions describes a local file pushed to a storage.
type UploadOptions struct {
	Content  string
	Filename string

	Checksum  string
	Algorithm string
}

// Fields renders the multipart form fields IN THE ORDER the node's parser
// expects them: content, checksum-algorithm, checksum. See MultipartField.
func (o UploadOptions) Fields() []MultipartField {
	fields := []MultipartField{{Name: "content", Value: o.Content}}
	if o.Checksum != "" {
		fields = append(fields,
			MultipartField{Name: "checksum-algorithm", Value: o.Algorithm},
			MultipartField{Name: "checksum", Value: o.Checksum},
		)
	}
	return fields
}

// Values renders the same thing as a plain payload, for --dry-run: a multipart
// body is not printable, but what it carries is.
func (o UploadOptions) Values() url.Values {
	v := url.Values{"content": {o.Content}, "filename": {o.Filename}}
	if o.Checksum != "" {
		v.Set("checksum", o.Checksum)
		v.Set("checksum-algorithm", o.Algorithm)
	}
	return v
}

// UploadFile pushes a local file to a storage and returns the UPID.
//
// Here the bytes DO travel through the machine running this CLI: local file →
// pveproxy → /var/tmp/pveupload-… → storage. That is the whole difference with
// DownloadURL, and it is the reason to prefer download-url whenever the file
// is reachable by a URL.
//
// POST /nodes/{node}/storage/{storage}/upload
func (c *Client) UploadFile(ctx context.Context, node, storage string, o UploadOptions, size int64, body io.Reader) (string, error) {
	var upid string
	err := c.postMultipart(ctx, epStorageUpload, []string{node, storage},
		o.Fields(), o.Filename, size, body, &upid)
	if err != nil {
		return "", err
	}
	return upid, nil
}

// DeleteVolume removes one volume from a storage and returns the UPID, or the
// empty string when the node finished within its own grace delay.
//
// The volume identifier is a VOLID — "local:iso/debian.iso" — not a filesystem
// path, and it travels inside the path, so its ':' and '/' are what the
// endpoint escaping exists for.
//
// DELETE /nodes/{node}/storage/{storage}/content/{volume}
func (c *Client) DeleteVolume(ctx context.Context, node, storage, volid string) (string, error) {
	var upid string
	if err := c.del(ctx, epStorageVolume, []string{node, storage, volid}, nil, &upid); err != nil {
		return "", err
	}
	return upid, nil
}

// DownloadPath, UploadPath and VolumePath render the paths, for --dry-run.
func DownloadPath(node, storage string) string { return epStorageDownURL.Path(node, storage) }
func UploadPath(node, storage string) string   { return epStorageUpload.Path(node, storage) }
func VolumePath(node, storage, volid string) string {
	return epStorageVolume.Path(node, storage, volid)
}
