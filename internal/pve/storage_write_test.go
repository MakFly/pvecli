package pve

import (
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

// A volid carries a slash — "local:iso/debian.iso" — and PVE splits it itself.
// Escaping it to %2F earns « unable to parse directory volume name » from the
// node: a 500 that blames the name, not the encoding. Found against the lab,
// pinned here so it cannot come back.
func TestVolumePathKeepsTheSlashOfAVolid(t *testing.T) {
	got := VolumePath("pve", "local", "local:iso/debian.iso")
	want := "/nodes/pve/storage/local/content/local:iso/debian.iso"
	if got != want {
		t.Errorf("VolumePath = %q, want %q", got, want)
	}
}

// Everywhere else, a slash inside a value must still be escaped: it would
// otherwise invent a path segment and address a different resource.
func TestOtherPlaceholdersStillEscapeTheSlash(t *testing.T) {
	if got := epNodeStatus.Path("a/b"); strings.Contains(got, "a/b") {
		t.Errorf("Path = %q — un '/' dans un nom de nœud doit être échappé", got)
	}
}

func TestDeleteVolumeSendsTheVolidInThePath(t *testing.T) {
	var method, path string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":"UPID:pve:0001845D:00251D3B:6A6D0BE5:imgdel:local:automation@pve!pvectl:"}`))
	})

	upid, err := c.DeleteVolume(context.Background(), "pve", "local", "local:iso/x.iso")
	if err != nil {
		t.Fatalf("DeleteVolume: %v", err)
	}
	if method != http.MethodDelete {
		t.Errorf("méthode = %s", method)
	}
	if path != "/api2/json/nodes/pve/storage/local/content/local:iso/x.iso" {
		t.Errorf("chemin = %q", path)
	}
	if !IsUPID(upid) {
		t.Errorf("DeleteVolume = %q, attendu un UPID", upid)
	}
}

func TestDownloadValuesPairsChecksumWithItsAlgorithm(t *testing.T) {
	v := DownloadOptions{
		URL: "https://example.invalid/x.iso", Content: "iso", Filename: "x.iso",
		Checksum: "abc", Algorithm: "sha256", SkipTLSVerify: true,
	}.Values()

	if v.Get("checksum") != "abc" || v.Get("checksum-algorithm") != "sha256" {
		t.Errorf("payload = %v", v)
	}
	// The node's schema declares each as « Requires option(s) » of the other:
	// one without the other is a 400 on a parameter that looks present.
	if v.Has("checksum") != v.Has("checksum-algorithm") {
		t.Error("checksum et checksum-algorithm doivent voyager ensemble")
	}
	if v.Get("verify-certificates") != "0" {
		t.Errorf("verify-certificates = %q", v.Get("verify-certificates"))
	}

	bare := DownloadOptions{URL: "u", Content: "iso", Filename: "f"}.Values()
	if bare.Has("checksum") || bare.Has("verify-certificates") {
		t.Errorf("payload sans option = %v, il ne doit rien inventer", bare)
	}
}

// The node does not parse a multipart body with a general-purpose parser:
// PVE::APIServer::AnyEvent walks it as a state machine, extracting `content`,
// then `checksum-algorithm`, then `checksum`, each anchored at the start of
// the remaining buffer. Out of order, the fields are silently dropped.
func TestUploadFieldsAreOrderedTheWayTheNodeParsesThem(t *testing.T) {
	fields := UploadOptions{
		Content: "iso", Filename: "x.iso", Checksum: "abc", Algorithm: "sha256",
	}.Fields()

	var names []string
	for _, f := range fields {
		names = append(names, f.Name)
	}
	want := []string{"content", "checksum-algorithm", "checksum"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("ordre des champs = %v, want %v", names, want)
	}

	// No checksum: the two fields disappear together, and content stays first.
	bare := UploadOptions{Content: "vztmpl", Filename: "x"}.Fields()
	if len(bare) != 1 || bare[0].Name != "content" {
		t.Errorf("champs sans checksum = %+v", bare)
	}
}

// The whole point of the upload primitive: the file part is named "filename",
// the body announces its length, and the fields arrive in order.
func TestUploadFileBuildsAParsableBody(t *testing.T) {
	var (
		contentType   string
		contentLength int64
		chunked       bool
		order         []string
		fileField     string
		fileName      string
		fileBytes     int
	)

	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		contentLength = r.ContentLength
		for _, e := range r.TransferEncoding {
			if e == "chunked" {
				chunked = true
			}
		}

		_, params, err := mime.ParseMediaType(contentType)
		if err != nil {
			t.Errorf("Content-Type illisible : %v", err)
			return
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			order = append(order, part.FormName())
			if part.FileName() != "" {
				fileField, fileName = part.FormName(), part.FileName()
				b, _ := io.ReadAll(part)
				fileBytes = len(b)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":"UPID:pve:00018334:002503F1:6A6D0BA5:imgcopy::automation@pve!pvectl:"}`))
	})

	payload := strings.Repeat("x", 4096)
	o := UploadOptions{Content: "iso", Filename: "x.iso", Checksum: "abc", Algorithm: "sha256"}

	upid, err := c.UploadFile(context.Background(), "pve", "local", o,
		int64(len(payload)), strings.NewReader(payload))
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if !IsUPID(upid) {
		t.Errorf("UploadFile = %q, attendu un UPID", upid)
	}

	if !strings.HasPrefix(contentType, "multipart/form-data") {
		t.Errorf("Content-Type = %q", contentType)
	}
	// PVE's own HTTP server answers « 501 — chunked transfer encoding not
	// supported ». The length has to be known before the first byte goes out.
	if chunked || contentLength <= 0 {
		t.Errorf("corps chunked = %v, Content-Length = %d — le nœud refuse le chunked",
			chunked, contentLength)
	}
	if got := strings.Join(order, ","); got != "content,checksum-algorithm,checksum,filename" {
		t.Errorf("ordre des parties = %q", got)
	}
	// « wrong field name … expected 'filename' », dit le nœud, sinon.
	if fileField != "filename" {
		t.Errorf("la partie fichier s'appelle %q, le nœud exige « filename »", fileField)
	}
	if fileName != "x.iso" {
		t.Errorf("filename= %q", fileName)
	}
	if fileBytes != len(payload) {
		t.Errorf("%d octets reçus, %d envoyés", fileBytes, len(payload))
	}
}
