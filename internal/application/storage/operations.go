package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"

	"haeryong.local/haeryong/internal/domain"
)

// Authority is mandatory even for replay and saved response visibility. The
// future HTTP/session adapter supplies this port; fixtures use labelled fakes.
type Authority interface {
	Authorize(context.Context, domain.Request) error
	AuthorizeWrite(context.Context, WriteTx, domain.Request) error
	Filter(context.Context, domain.Request, domain.Response) (domain.Response, error)
}

func Execute(ctx context.Context, store Store, authority Authority, request domain.Request, normalize func() ([]byte, error), apply func(WriteTx, domain.Receipt) (domain.Response, error)) (domain.Response, error) {
	return ExecuteNormalized(ctx, store, authority, request, func(tx WriteTx) ([]byte, error) {
		if normalize == nil {
			return nil, nil
		}
		return normalize()
	}, apply)
}

// ExecuteNormalized captures defaults using the already-owned writer transaction.
func ExecuteNormalized(ctx context.Context, store Store, authority Authority, request domain.Request, normalize func(WriteTx) ([]byte, error), apply func(WriteTx, domain.Receipt) (domain.Response, error)) (domain.Response, error) {
	if authority == nil {
		return domain.Response{}, domain.ErrDenied
	}
	if err := authority.Authorize(ctx, request); err != nil {
		return domain.Response{}, err
	}
	var response domain.Response
	err := store.Write(ctx, func(tx WriteTx) error {
		if err := authority.AuthorizeWrite(ctx, tx, request); err != nil {
			return err
		}
		receipt, existing, err := tx.ReserveOperation(request, func() ([]byte, error) {
			if normalize == nil {
				return nil, nil
			}
			return normalize(tx)
		})
		if err != nil {
			return err
		}
		if existing {
			if receipt.State == "completed" || receipt.State == "failed" {
				response = *receipt.Response
				return nil
			}
			if receipt.State == "reconciling" {
				return domain.ErrReconciliation
			}
			response = domain.Response{Status: 202, Body: jsonBytesPending(request.Key.OperationID)}
			return nil
		}
		response, err = apply(tx, receipt)
		if err != nil {
			return err
		}
		if err := authority.AuthorizeWrite(ctx, tx, request); err != nil {
			return err
		}
		return tx.CompleteOperation(request.Key, response)
	})
	if err != nil {
		return domain.Response{}, err
	}
	return authority.Filter(ctx, request, response)
}

func Fingerprint(r domain.Request, normalization []byte) (string, error) {
	if r.Key.PrincipalID == "" || !operationID.MatchString(r.Key.OperationID) || r.Method == "" || r.Target == "" {
		return "", errors.New("operation identity/method/actual target required")
	}
	omitted := append([]string{}, r.Omitted...)
	sort.Strings(omitted)
	for i, v := range omitted {
		if v == "" || (i > 0 && omitted[i-1] == v) {
			return "", errors.New("invalid omission markers")
		}
	}
	preconditions := map[string]string{}
	for key, value := range r.Preconditions {
		normalized := strings.ToLower(key)
		if normalized == "" {
			return "", errors.New("empty semantic precondition")
		}
		if _, exists := preconditions[normalized]; exists {
			return "", errors.New("duplicate semantic precondition")
		}
		preconditions[normalized] = value
	}
	body := r.Body
	if json.Valid(body) {
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return "", err
		}
		canonical, err := json.Marshal(value)
		if err != nil {
			return "", err
		}
		body = canonical
	}
	data, err := json.Marshal(struct {
		Method, Target string
		Body           []byte
		Preconditions  map[string]string
		Omitted        []string
		Normalization  []byte
	}{strings.ToUpper(r.Method), r.Target, body, preconditions, omitted, normalization})
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

func jsonBytesPending(id string) []byte {
	body, _ := json.Marshal(map[string]string{"operation_id": id, "state": "accepted", "operation_url": "/api/v1/operations/" + id})
	return body
}

var operationID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Replay checks an existing receipt under current authority without reserving new
// work. Preparation use cases can avoid redoing fresh-admission verification.
func Replay(ctx context.Context, store Store, authority Authority, request domain.Request) (domain.Response, bool, error) {
	if authority == nil {
		return domain.Response{}, false, domain.ErrDenied
	}
	if err := authority.Authorize(ctx, request); err != nil {
		return domain.Response{}, false, err
	}
	var response domain.Response
	found := false
	err := store.Write(ctx, func(tx WriteTx) error {
		if err := authority.AuthorizeWrite(ctx, tx, request); err != nil {
			return err
		}
		receipt, err := tx.Receipt(request.Key)
		if errors.Is(err, domain.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		if _, err := CheckPurgedReceipt(request, receipt); err != nil {
			return err
		}
		fingerprint, err := Fingerprint(request, receipt.Normalization)
		if err != nil {
			return err
		}
		if fingerprint != receipt.Fingerprint {
			return domain.ErrConflict
		}
		switch receipt.State {
		case "completed", "failed":
			response = *receipt.Response
		case "reconciling":
			return domain.ErrReconciliation
		default:
			response = domain.Response{Status: 202, Body: jsonBytesPending(request.Key.OperationID)}
		}
		return nil
	})
	if err != nil || !found {
		return domain.Response{}, found, err
	}
	response, err = authority.Filter(ctx, request, response)
	return response, true, err
}

// CheckPurgedReceipt preserves comparison without retaining a purged private
// request. Even an equivalent call cannot redispatch or disclose the old result.
func CheckPurgedReceipt(request domain.Request, receipt domain.Receipt) (bool, error) {
	var redacted struct {
		Document string `json:"purged_document_id"`
		Request  string `json:"request_sha256"`
	}
	if json.Unmarshal(receipt.Normalization, &redacted) != nil || redacted.Document == "" {
		return false, nil
	}
	digest, err := Fingerprint(request, nil)
	if err != nil {
		return true, err
	}
	if digest != redacted.Request {
		return true, domain.ErrConflict
	}
	return true, domain.ErrNotFound
}
