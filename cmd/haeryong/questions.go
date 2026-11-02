package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"haeryong.local/haeryong/internal/adapters/client"
	"haeryong.local/haeryong/internal/adapters/clientstate"
	"haeryong.local/haeryong/internal/application/questions"
	"haeryong.local/haeryong/internal/application/service"
	"haeryong.local/haeryong/internal/application/transfer"
	"haeryong.local/haeryong/internal/domain"
)

func questionCommand(ctx context.Context, opt options, emit func(any) error) (resultErr error) {
	if opt.command[0] == "questions" {
		return readQuestions(ctx, opt, emit)
	}
	if len(opt.command) != 1 || !operationID.MatchString(opt.flags["version"]) || opt.flags["request-file"] == "" {
		return invalid("ask requires --version ID --request-file PATH")
	}
	api, state, e := selectedClient(ctx, opt)
	if e != nil {
		return e
	}
	identity, e := api.Identity(ctx)
	if e != nil {
		return productError(e)
	}
	path, e := filepath.Abs(opt.flags["request-file"])
	if e != nil {
		return e
	}
	target := "/api/v1/versions/" + opt.flags["version"] + "/questions"
	// Explicit selectors belong to this pending command, rather than the next
	// mutable Reader/default state or a newly edited request file.
	selectionJSON, _ := json.Marshal(map[string]string{"page": opt.flags["page"], "region": opt.flags["region"], "source_mode": opt.flags["source-mode"]})
	intent := clientstate.CommandIntent{Method: "POST", Target: target, SourcePath: path, QuestionSelectors: string(selectionJSON)}
	k := opt.flags["operation-id"]
	automatic := k == ""
	if automatic {
		k, e = service.NewID()
		if e != nil {
			return e
		}
		k, e = state.UseCommandPending(ctx, identity, intent, k)
		if e != nil {
			return e
		}
	}
	if !operationID.MatchString(k) {
		return invalid("canonical operation ID required")
	}
	defer func() {
		var recorded *recordedCommandError
		if resultErr != nil && !errors.As(resultErr, &recorded) {
			resultErr = &pendingReviewError{cause: resultErr, operation: k}
		}
	}()
	unlock, e := state.Lock(ctx, k)
	if e != nil {
		return e
	}
	j, e := state.Load(k)
	if errors.Is(e, domain.ErrNotFound) {
		j, e = captureQuestion(ctx, api, state, identity, opt, path, target, k)
	} else if e == nil {
		if j.Purpose != "question" || j.Question == nil || j.Question.SourcePath != path || j.Question.Selectors != string(selectionJSON) || j.Target != target || j.Identity.LibraryID != identity.LibraryID || j.Identity.PrincipalID != identity.PrincipalID {
			e = transfer.ErrIntent
		}
	}
	if e != nil {
		_ = unlock()
		return productError(e)
	}
	var acknowledge func() error
	if automatic {
		acknowledge = func() error { return state.FinishCommandPending(context.Background(), j.Question.Launcher, intent, k) }
	}
	if j.FinalBody != nil {
		response, lookup := transfer.AwaitOperation(ctx, api, k)
		if lookup == nil {
			j.Response, j.Phase = &response, "accepted"
			e = state.Save(j)
		} else if !errors.Is(lookup, domain.ErrNotFound) {
			if response.Status >= 400 {
				j.Response, j.Phase = &response, "failed"
				if e = state.Save(j); e != nil {
					_ = unlock()
					return e
				}
			}
			if e = unlock(); e != nil {
				return e
			}
			return afterDefinitive(recordedError(productError(lookup), response, k, nil), k, acknowledge)
		} else if j.Response != nil {
			e = transfer.ErrReconcile
		}
		if e != nil {
			_ = unlock()
			return productError(e)
		}
	}
	if j.Response == nil && j.FinalBody == nil {
		var request questions.Request
		e = json.Unmarshal(j.Question.Request, &request)
		if e != nil {
			_ = unlock()
			return productError(transfer.ErrIntent)
		}
		request.SourcesPresent = j.Question.SourcesPresent
		if j.Question.Context != nil {
			if j.Question.ContextRef == "" {
				upload, ref, err := transfer.New(api, state).PrepareQuestionContext(ctx, &j, request.PagePath)
				if err != nil {
					_ = unlock()
					return productError(err)
				}
				if j.Question.ContextKey == "" {
					j.Question.ContextKey, err = service.NewID()
					if err != nil {
						_ = unlock()
						return err
					}
					if err = state.Save(j); err != nil {
						_ = unlock()
						return err
					}
				}
				values := url.Values{"input": {"upload"}, "version_id": {j.VersionID}}
				if request.PagePath != nil {
					values.Set("page_path", *request.PagePath)
				}
				// The separate mutation owns another operation lock. Its receipt is
				// reconciled before any replay; no credential enters these bytes.
				contextResponse, _, err := mutationConditions(ctx, api, state, j.Question.ContextKey, "POST", "/api/v1/contexts?"+values.Encode(), map[string]string{"upload_id": upload}, nil)
				if err != nil {
					_ = unlock()
					return productError(err)
				}
				var context struct {
					ID    string `json:"context_ref"`
					Hash  string `json:"bytes_sha256"`
					Media string `json:"media_type"`
				}
				if json.Unmarshal(contextResponse.Body, &context) != nil || context.ID == "" || context.Hash != ref.Hash || context.Media != "application/json" {
					_ = unlock()
					return productError(transfer.ErrReconcile)
				}
				j.Question.ContextRef = context.ID
				if e = state.Save(j); e != nil {
					_ = unlock()
					return e
				}
			}
			request.ContextRef = &j.Question.ContextRef
		}
		if request.BodyRef == nil {
			ref, err := transfer.New(api, state).PrepareQuestionText(ctx, &j, request.PagePath)
			if err != nil {
				_ = unlock()
				return productError(err)
			}
			request.BodyRef = &domain.BodyReference{ID: ref.ID, Hash: ref.Hash, Size: ref.Size, MediaType: ref.MediaType}
		}
		request.Question = ""
		j.FinalBody, e = questions.MarshalRequest(request)
		if e != nil {
			_ = unlock()
			return e
		}
		j.Phase = "final_dispatch"
		if e = state.Save(j); e != nil {
			_ = unlock()
			return e
		}
	}
	if e = unlock(); e != nil {
		return e
	}
	if j.Response == nil {
		response, id, e := mutationConditions(ctx, api, state, k, "POST", target, json.RawMessage(j.FinalBody), nil)
		if e != nil {
			return afterDefinitive(recordedError(productError(e), response, id, nil), id, acknowledge)
		}
		j.Response = &response
	}
	return followQuestion(ctx, api, state, j, intent, automatic, emit)
}

func captureQuestion(ctx context.Context, api *client.Client, state *clientstate.Store, identity transfer.Identity, opt options, path, target, k string) (transfer.Journal, error) {
	var j transfer.Journal
	file, e := os.Open(path)
	if e != nil {
		return j, e
	}
	request, e := questions.DecodeCLIRequest(file)
	e = errors.Join(e, file.Close())
	if e != nil {
		return j, commandError{2, e}
	}
	if page := opt.flags["page"]; page != "" {
		if request.PagePath != nil && *request.PagePath != page {
			return j, transfer.ErrIntent
		}
		request.PagePath = &page
	}
	if region := opt.flags["region"]; region != "" {
		if request.RegionID != nil && *request.RegionID != region {
			return j, transfer.ErrIntent
		}
		request.RegionID = &region
	}
	if mode := opt.flags["source-mode"]; mode != "" {
		if mode != "pinned" && mode != "current" || request.SourceMode != "" && request.SourceMode != mode || request.Sources.SourceMode != "" && request.Sources.SourceMode != mode {
			return j, transfer.ErrIntent
		}
		request.SourceMode = mode
	}
	if request.RegionID != nil && request.PagePath == nil {
		return j, invalid("region requires its exact page")
	}
	var version struct {
		DocumentID string `json:"document_id"`
	}
	if _, e = api.JSON(ctx, "GET", "/api/v1/versions/"+opt.flags["version"], "", nil, &version); e != nil {
		return j, e
	}
	selectors, _ := json.Marshal(map[string]string{"page": opt.flags["page"], "region": opt.flags["region"], "source_mode": opt.flags["source-mode"]})
	j = transfer.Journal{OperationID: k, Purpose: "question", Method: "POST", Target: target, Identity: identity, VersionID: opt.flags["version"], Phase: "capturing", Question: &transfer.QuestionLaunch{SourcePath: path, Launcher: identity, SourcesPresent: request.SourcesPresent, Selectors: string(selectors)}}
	if request.DisplayState != nil {
		snapshot, e := captureQuestionBytes(ctx, state, request.DisplayState, "question display state")
		if e != nil {
			return j, e
		}
		j.Question.Context = &transfer.Preparation{Snapshot: snapshot}
		request.DisplayState = nil
	}
	if request.BodyRef == nil {
		if strings.TrimSpace(request.Question) == "" {
			return j, invalid("question text is required")
		}
		// This temporary serialization is not durable caller state. The ordinary
		// protected snapshot owns complete verified bytes before dispatch.
		snapshot, e := captureQuestionBytes(ctx, state, []byte(request.Question), "question text")
		if e != nil {
			return j, e
		}
		j.Result.Snapshot = snapshot
		request.Question = ""
	}
	// The typed preparation journal retains selectors; the complete original
	// question lives in its verified local snapshot until body upload finishes.
	j.Question.Request, e = questions.MarshalRequest(request)
	if e != nil {
		return j, e
	}
	if e = state.Save(j); e != nil {
		return j, e
	}
	return j, nil
}

func captureQuestionBytes(ctx context.Context, state *clientstate.Store, raw []byte, name string) (transfer.Snapshot, error) {
	temp, e := os.CreateTemp("/tmp", "haeryong-question-*")
	if e != nil {
		return transfer.Snapshot{}, e
	}
	path := temp.Name()
	defer os.Remove(path)
	_, e = temp.Write(raw)
	e = errors.Join(e, temp.Sync(), temp.Close())
	if e != nil {
		return transfer.Snapshot{}, e
	}
	snapshot, e := state.Capture(ctx, path)
	if e != nil {
		return snapshot, e
	}
	snapshot.Mode = "object"
	snapshot.SelectionName = name
	return snapshot, nil
}

func followQuestion(ctx context.Context, api *client.Client, state *clientstate.Store, j transfer.Journal, intent clientstate.CommandIntent, automatic bool, emit func(any) error) error {
	if j.Response == nil {
		return productError(transfer.ErrReconcile)
	}
	var accepted struct {
		Question string `json:"question_id"`
		Job      string `json:"job_id"`
	}
	if json.Unmarshal(j.Response.Body, &accepted) != nil || accepted.Question == "" || accepted.Job == "" {
		return productError(transfer.ErrReconcile)
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		job, e := api.Job(ctx, accepted.Job)
		if e != nil {
			return productError(e)
		}
		switch job.State {
		case "completed", "failed", "cancelled", "paused":
			var question questions.QuestionView
			if _, e = api.JSON(ctx, "GET", "/api/v1/questions/"+accepted.Question, "", nil, &question); e != nil {
				return productError(e)
			}
			saved, e := questionReadOutput(ctx, api, question)
			if e != nil {
				return productError(e)
			}
			if e = emit(map[string]any{"operation_id": j.OperationID, "result": json.RawMessage(j.Response.Body), "job_outcome": job, "saved": saved}); e != nil {
				return e
			}
			if job.ProviderAttempts != nil && job.ProviderAttempts.Unknown > 0 {
				return commandError{6, fmt.Errorf("question outcome unknown")}
			}
			if job.State == "paused" {
				return commandError{5, fmt.Errorf("question paused: %s", job.Error)}
			}
			if automatic {
				if e = state.FinishCommandPending(ctx, j.Question.Launcher, intent, j.OperationID); e != nil {
					return e
				}
			}
			if job.State == "completed" {
				return nil
			}
			return commandError{5, fmt.Errorf("question %s: %s", job.State, job.Error)}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
func readQuestions(ctx context.Context, opt options, emit func(any) error) error {
	if len(opt.command) < 2 {
		return invalid("questions list/show")
	}
	api, _, e := selectedClient(ctx, opt)
	if e != nil {
		return e
	}
	if opt.command[1] == "show" {
		if len(opt.command) != 3 || !operationID.MatchString(opt.command[2]) {
			return invalid("questions show requires ID")
		}
		var value questions.QuestionView
		if _, e = api.JSON(ctx, "GET", "/api/v1/questions/"+opt.command[2], "", nil, &value); e != nil {
			return productError(e)
		}
		output, e := questionReadOutput(ctx, api, value)
		if e != nil {
			return productError(e)
		}
		return emit(output)
	}
	if opt.command[1] != "list" || len(opt.command) != 2 || !operationID.MatchString(opt.flags["version"]) {
		return invalid("questions list requires --version ID")
	}
	cursor := opt.flags["cursor"]
	for {
		path := "/api/v1/versions/" + opt.flags["version"] + "/questions"
		if cursor != "" {
			path += "?cursor=" + url.QueryEscape(cursor)
		}
		var page questions.Page[questions.QuestionView]
		if _, e = api.JSON(ctx, "GET", path, "", nil, &page); e != nil {
			return productError(e)
		}
		for _, item := range page.Items {
			if e = emit(item); e != nil {
				return e
			}
		}
		if page.NextCursor == nil {
			return nil
		}
		cursor = *page.NextCursor
	}
}

// Every saved answer page and complete body is read without dispatching a
// provider. This is shared by first delivery and later explicit saved reopen.
func questionReadOutput(ctx context.Context, api *client.Client, value questions.QuestionView) (map[string]any, error) {
	text, e := readQuestionText(ctx, api, value.ID, false, value.BodyRef)
	if e != nil {
		return nil, e
	}
	output := map[string]any{"question": value, "text": text}
	if value.Context != nil {
		expected := domain.BodyReference{ID: value.ID, Hash: value.Context.Hash, Size: fmt.Sprint(value.Context.Size), MediaType: value.Context.MediaType}
		full, e := readQuestionObject(ctx, api, "/api/v1/questions/"+value.ID+"/context", expected)
		if e != nil {
			return nil, e
		}
		output["display_context"] = full
	}
	answers := []map[string]any{}
	page := value.Answers
	for page != nil {
		for _, a := range page.Items {
			body, e := readQuestionText(ctx, api, a.ID, true, a.BodyRef)
			if e != nil {
				return nil, e
			}
			answers = append(answers, map[string]any{"answer": a, "body": body})
		}
		if page.NextCursor == nil {
			break
		}
		var next questions.Page[questions.AnswerView]
		if _, e = api.JSON(ctx, "GET", "/api/v1/questions/"+value.ID+"/answers?cursor="+url.QueryEscape(*page.NextCursor), "", nil, &next); e != nil {
			return nil, e
		}
		page = &next
	}
	output["answers"] = answers
	return output, nil
}

func readQuestionText(ctx context.Context, api *client.Client, id string, answer bool, expected domain.BodyReference) (string, error) {
	if !operationID.MatchString(id) || expected.ID != id {
		return "", domain.ErrContentIntegrity
	}
	path := "/api/v1/questions/" + id + "/text"
	if answer {
		path = "/api/v1/answers/" + id + "/body"
	}
	return readQuestionObject(ctx, api, path, expected)
}
func readQuestionObject(ctx context.Context, api *client.Client, path string, expected domain.BodyReference) (string, error) {
	response, e := api.Call(ctx, "GET", path, "", nil, nil)
	if e != nil {
		return "", e
	}
	defer response.Body.Close()
	if response.Header.Get("ETag") != `"sha256-`+expected.Hash+`"` {
		return "", domain.ErrContentIntegrity
	}
	raw, e := io.ReadAll(response.Body)
	if e != nil {
		return "", e
	}
	hash := sha256.Sum256(raw)
	if !utf8.Valid(raw) || fmt.Sprint(len(raw)) != expected.Size || hex.EncodeToString(hash[:]) != expected.Hash {
		return "", domain.ErrContentIntegrity
	}
	return string(raw), nil
}
