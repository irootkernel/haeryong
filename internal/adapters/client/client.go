// Package client is the narrow authenticated API transport. Credentials are
// selected from protected files and are never forwarded across an origin.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	importer "haeryong.local/haeryong/internal/application/import"
	"haeryong.local/haeryong/internal/application/transfer"
	"haeryong.local/haeryong/internal/domain"
	sec "haeryong.local/haeryong/internal/security/session"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Error struct {
	Status  int
	Code    string
	Details json.RawMessage
}

func (e *Error) Error() string { return e.Code }
func (e *Error) Unwrap() error {
	if e.Code == "OPERATION_RECONCILIATION_REQUIRED" || e.Code == "PROVIDER_OUTCOME_UNKNOWN" {
		return transfer.ErrReconcile
	}
	switch e.Status {
	case 404:
		return domain.ErrNotFound
	case 409:
		return domain.ErrConflict
	case 412:
		return domain.ErrPrecondition
	case 403:
		return domain.ErrDenied
	}
	return nil
}
func IsNotFound(e error) bool { var api *Error; return errors.As(e, &api) && api.Status == 404 }

type Credential struct {
	Endpoint  string `json:"endpoint"`
	Profile   string `json:"profile"`
	LibraryID string `json:"library_id"`
	Token     string `json:"token"`
}
type Client struct {
	origin     string
	credential Credential
	http       *http.Client
}

var operationIdentity = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func Origin(raw string) (string, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || strings.ContainsAny(raw, "* \r\n\t") {
		return "", fmt.Errorf("exact endpoint origin required")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")) {
		return "", fmt.Errorf("HTTPS or explicit loopback endpoint required")
	}
	return u.Scheme + "://" + u.Host, nil
}
func LoadCredential(path string) (Credential, error) {
	var c Credential
	info, e := os.Lstat(path)
	if e != nil {
		return c, e
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return c, fmt.Errorf("credential must be a protected regular file")
	}
	f, e := os.Open(path)
	if e != nil {
		return c, e
	}
	defer f.Close()
	actual, e := f.Stat()
	if e != nil || !os.SameFile(info, actual) {
		return c, fmt.Errorf("credential changed while opening")
	}
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if e = d.Decode(&c); e != nil {
		return c, e
	}
	if !sec.ValidSecret(c.Token) || c.Profile == "" || c.LibraryID == "" {
		return c, fmt.Errorf("credential binding missing")
	}
	return c, nil
}
func New(endpoint, profile string, c Credential) (*Client, error) {
	origin, e := Origin(endpoint)
	if e != nil {
		return nil, e
	}
	bound, e := Origin(c.Endpoint)
	if e != nil || origin != bound || c.Profile != profile || !sec.ValidSecret(c.Token) || c.LibraryID == "" {
		return nil, fmt.Errorf("credential endpoint/profile/library binding mismatch")
	}
	return &Client{origin: origin, credential: c, http: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("API redirect rejected") }}}, nil
}
func (c *Client) Call(ctx context.Context, method, path, key string, body io.Reader, headers map[string]string) (*http.Response, error) {
	if !strings.HasPrefix(path, "/api/v1/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "\r\n") {
		return nil, fmt.Errorf("invalid API path")
	}
	target, e := url.Parse(c.origin + path)
	if e != nil || target.Scheme+"://"+target.Host != c.origin {
		return nil, fmt.Errorf("API origin mismatch")
	}
	r, e := http.NewRequestWithContext(ctx, method, target.String(), body)
	if e != nil {
		return nil, e
	}
	r.Header.Set("Authorization", "Bearer "+c.credential.Token)
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	for k, v := range headers {
		if strings.EqualFold(k, "Authorization") || strings.EqualFold(k, "Host") {
			return nil, fmt.Errorf("credential header override rejected")
		}
		r.Header.Set(k, v)
	}
	response, e := c.http.Do(r)
	if e != nil {
		return nil, e
	}
	if response.StatusCode >= 400 {
		defer response.Body.Close()
		var envelope struct {
			Error struct {
				Code    string          `json:"code"`
				Details json.RawMessage `json:"details"`
			} `json:"error"`
		}
		if e = json.NewDecoder(response.Body).Decode(&envelope); e != nil {
			return nil, fmt.Errorf("API failure HTTP %d", response.StatusCode)
		}
		return nil, &Error{response.StatusCode, envelope.Error.Code, envelope.Error.Details}
	}
	if response.StatusCode >= 300 && response.StatusCode != 304 {
		response.Body.Close()
		return nil, fmt.Errorf("unexpected redirect")
	}
	return response, nil
}
func (c *Client) JSON(ctx context.Context, method, path, key string, value, out any) (domain.Response, error) {
	var input io.Reader
	if value != nil {
		b, e := json.Marshal(value)
		if e != nil {
			return domain.Response{}, e
		}
		input = bytes.NewReader(b)
	}
	r, e := c.Call(ctx, method, path, key, input, map[string]string{"Content-Type": "application/json"})
	if e != nil {
		return domain.Response{}, e
	}
	defer r.Body.Close()
	if r.StatusCode == http.StatusNoContent {
		return domain.Response{Status: r.StatusCode}, nil
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if e = json.NewDecoder(r.Body).Decode(&env); e != nil {
		return domain.Response{}, e
	}
	response := domain.Response{Status: r.StatusCode, Body: env.Data, Headers: map[string]string{"ETag": r.Header.Get("ETag")}}
	if r.StatusCode == http.StatusAccepted && key != "" {
		var pending struct {
			ID    string `json:"operation_id"`
			State string `json:"state"`
			URL   string `json:"operation_url"`
		}
		if e = json.Unmarshal(env.Data, &pending); e != nil {
			return domain.Response{}, e
		}
		if pending.State == "accepted" {
			if !operationIdentity.MatchString(key) || pending.ID != key || pending.URL != "/api/v1/operations/"+key {
				return domain.Response{}, transfer.ErrReconcile
			}
			// The server URL is only checked as a contract field. Poll constructs
			// its own same-origin path from the locally retained operation ID.
			if e = r.Body.Close(); e != nil {
				return domain.Response{}, e
			}
			response, e = transfer.AwaitOperation(ctx, c, key)
			if e != nil {
				return response, e
			}
		}
	}
	if out != nil {
		if e = json.Unmarshal(response.Body, out); e != nil {
			return domain.Response{}, e
		}
	}
	return response, nil
}
func (c *Client) Identity(ctx context.Context) (transfer.Identity, error) {
	var out transfer.Identity
	var caps struct {
		Library struct {
			ID      string `json:"library_id"`
			Unfiled string `json:"unfiled_project_id"`
			Runtime string `json:"runtime_generation"`
		} `json:"library"`
		LibraryID string `json:"library_id"`
		Unfiled   string `json:"unfiled_project_id"`
		Runtime   string `json:"runtime_generation"`
	}
	_, e := c.JSON(ctx, "GET", "/api/v1/capabilities", "", nil, &caps)
	if e != nil {
		return out, e
	}
	out.LibraryID = caps.LibraryID
	out.UnfiledProjectID = caps.Unfiled
	out.RuntimeGeneration = caps.Runtime
	if out.LibraryID == "" {
		out.LibraryID = caps.Library.ID
		out.UnfiledProjectID = caps.Library.Unfiled
		out.RuntimeGeneration = caps.Library.Runtime
	}
	if out.LibraryID != c.credential.LibraryID {
		return out, fmt.Errorf("endpoint library identity mismatch")
	}
	var session domain.Session
	_, e = c.JSON(ctx, "GET", "/api/v1/session", "", nil, &session)
	if e != nil {
		return out, e
	}
	if session.LibraryID != out.LibraryID {
		return out, fmt.Errorf("session library identity mismatch")
	}
	out.PrincipalID = session.PrincipalID
	if session.Scope.Kind != "owner" {
		out.UnfiledProjectID = ""
		if len(session.Scope.ProjectIDs) == 1 {
			out.SelectedProjectID = session.Scope.ProjectIDs[0]
		}
	}
	return out, nil
}
func (c *Client) Document(ctx context.Context, id string) (transfer.Document, error) {
	var out transfer.Document
	_, e := c.JSON(ctx, "GET", "/api/v1/documents/"+id, "", nil, &out)
	return out, e
}
func (c *Client) Operation(ctx context.Context, id string) (domain.Response, error) {
	var receipt struct {
		Status *int            `json:"original_status"`
		Result json.RawMessage `json:"result_metadata"`
		State  string          `json:"state"`
	}
	_, e := c.JSON(ctx, "GET", "/api/v1/operations/"+id, "", nil, &receipt)
	if e != nil {
		return domain.Response{}, e
	}
	if receipt.Status == nil {
		switch receipt.State {
		case "accepted":
			return domain.Response{}, transfer.ErrPending
		case "reconciling":
			return domain.Response{}, transfer.ErrReconcile
		default:
			return domain.Response{}, fmt.Errorf("definitive operation response missing")
		}
	}
	if receipt.State != "completed" && receipt.State != "failed" {
		return domain.Response{}, fmt.Errorf("invalid operation response state")
	}
	response := domain.Response{Status: *receipt.Status, Body: receipt.Result}
	if response.Status >= 400 {
		var failure struct {
			Error struct {
				Code    string          `json:"code"`
				Details json.RawMessage `json:"details"`
			} `json:"error"`
		}
		if e := json.Unmarshal(receipt.Result, &failure); e != nil || failure.Error.Code == "" {
			return response, fmt.Errorf("recorded failure response is malformed")
		}
		return response, &Error{Status: response.Status, Code: failure.Error.Code, Details: failure.Error.Details}
	}
	return response, nil
}
func (c *Client) Create(ctx context.Context, key string, input importer.CreateRequest) (transfer.Created, error) {
	var out transfer.Created
	_, e := c.JSON(ctx, "POST", "/api/v1/uploads", key, input, &out)
	return out, e
}
func (c *Client) Upload(ctx context.Context, id string) (importer.UploadDetail, error) {
	var out importer.UploadDetail
	_, e := c.JSON(ctx, "GET", "/api/v1/uploads/"+id, "", nil, &out)
	return out, e
}
func (c *Client) Complete(ctx context.Context, id, key string, cp domain.Checkpoint) (domain.Response, error) {
	return c.JSON(ctx, "POST", "/api/v1/uploads/"+id+"/complete", key, map[string]any{"checkpoint": cp}, nil)
}
func (c *Client) Import(ctx context.Context, key string, body json.RawMessage) (domain.Response, error) {
	return c.JSON(ctx, "POST", "/api/v1/imports", key, body, nil)
}
func (c *Client) Validate(ctx context.Context, key string, body json.RawMessage) (domain.Response, error) {
	return c.JSON(ctx, "POST", "/api/v1/packages/validate", key, body, nil)
}
func (c *Client) Job(ctx context.Context, id string) (domain.Job, error) {
	var out domain.Job
	_, e := c.JSON(ctx, "GET", "/api/v1/jobs/"+id, "", nil, &out)
	return out, e
}
func (c *Client) Put(ctx context.Context, id string, chunk importer.Chunk, reader io.Reader) (importer.ChunkResult, error) {
	headers := map[string]string{"Content-Type": "application/octet-stream", "Haeryong-Upload-Offset": fmt.Sprint(chunk.Offset), "Haeryong-Upload-Chunk-SHA256": chunk.Hash}
	if chunk.Length != nil {
		headers["Haeryong-Upload-Length"] = fmt.Sprint(*chunk.Length)
	}
	if chunk.Final {
		headers["Haeryong-Upload-Final"] = "true"
	}
	if chunk.Kind != "file" {
		reader = bytes.NewReader([]byte(`{}`))
		if chunk.Kind == "symlink" {
			b, _ := json.Marshal(map[string]string{"link_target": chunk.LinkTarget})
			reader = bytes.NewReader(b)
		}
		headers = map[string]string{"Content-Type": "application/json"}
	}
	r, e := c.Call(ctx, "PUT", "/api/v1/uploads/"+id+"/entries?"+url.Values{"path": {chunk.Path}, "kind": {chunk.Kind}}.Encode(), "", reader, headers)
	if e != nil {
		return importer.ChunkResult{}, e
	}
	defer r.Body.Close()
	var out struct {
		Data importer.ChunkResult `json:"data"`
	}
	e = json.NewDecoder(r.Body).Decode(&out)
	return out.Data, e
}

func SaveCredential(path string, value Credential) error {
	if !sec.ValidSecret(value.Token) || value.Profile == "" || value.LibraryID == "" {
		return fmt.Errorf("credential binding missing")
	}
	if _, e := Origin(value.Endpoint); e != nil {
		return e
	}
	parent := filepath.Dir(path)
	info, e := os.Lstat(parent)
	if e != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("credential parent must be a protected directory")
	}
	if info, e := os.Lstat(path); e == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0) {
		return fmt.Errorf("unsafe credential destination")
	}
	f, e := os.CreateTemp(parent, ".credential-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	e = json.NewEncoder(f).Encode(value)
	e = errors.Join(e, f.Sync(), f.Close())
	if e != nil {
		return e
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return e
	}
	return syncDirectory(parent)
}
