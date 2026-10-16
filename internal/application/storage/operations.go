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
		receipt, existing, err := tx.ReserveOperation(request, normalize)
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
