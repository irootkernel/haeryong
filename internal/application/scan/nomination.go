package scan

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"haeryong.local/haeryong/internal/application/service"
	app "haeryong.local/haeryong/internal/application/storage"
	"haeryong.local/haeryong/internal/domain"
)

const (
	MaxNominationCardsPerBatch  = 64
	MaxNominationCardJSONBytes  = 8192
	MaxNominationFrameBytes     = 144 * 1024
	NominationFixedFrameReserve = 8192

	nominationHeadTailSeparator = "\n...\n"
)

type plannedNominationBatch struct {
	BatchID string
	Pass    int
	Items   []domain.JobItem
}

type aggregatedUnitNomination struct {
	Abstained          bool
	AbstainReason      string
	HasCompletedResult bool
	Candidates         []domain.MaintenanceNominatedCandidate
	VisitedBatchIDs    []string
}

type globalNominationStatus struct {
	Settled           bool
	Coverage          string
	TotalPlanned      int
	VisitedBatchIDs   []string
	ByItemID          map[string]aggregatedUnitNomination
	ItemByID          map[string]domain.JobItem
	EligibleUnitCount int
}

func truncateUTF8Bytes(s string, maxBytes int) (string, bool) {
	s = strings.TrimSpace(s)
	if maxBytes <= 0 {
		if s == "" {
			return "", false
		}
		return "", true
	}
	if len(s) <= maxBytes {
		return s, false
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return strings.TrimSpace(s[:cut]), true
}

func truncateUTF8HeadTailBytes(s string, maxBytes int) (string, bool) {
	s = strings.TrimSpace(s)
	if maxBytes <= 0 {
		if s == "" {
			return "", false
		}
		return "", true
	}
	if len(s) <= maxBytes {
		return s, false
	}
	if maxBytes <= len(nominationHeadTailSeparator) {
		return truncateUTF8Bytes(s, maxBytes)
	}
	avail := maxBytes - len(nominationHeadTailSeparator)
	headBudget := (avail * 3) / 5
	if headBudget < 1 {
		headBudget = 1
	}
	tailBudget := avail - headBudget
	if tailBudget < 1 {
		return truncateUTF8Bytes(s, maxBytes)
	}
	headEnd := headBudget
	for headEnd > 0 && !utf8.RuneStart(s[headEnd]) {
		headEnd--
	}
	tailStart := len(s) - tailBudget
	for tailStart < len(s) && !utf8.RuneStart(s[tailStart]) {
		tailStart++
	}
	head := strings.TrimSpace(s[:headEnd])
	tail := strings.TrimSpace(s[tailStart:])
	if head == "" || tail == "" || headEnd >= tailStart {
		return truncateUTF8Bytes(s, maxBytes)
	}
	return head + nominationHeadTailSeparator + tail, true
}

func truncateUTF8Slice(in []string, maxItems, maxItemBytes int) ([]string, bool) {
	if len(in) == 0 {
		return nil, false
	}
	truncated := false
	limit := len(in)
	if limit > maxItems {
		limit = maxItems
		truncated = true
	}
	out := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		val, t := truncateUTF8Bytes(in[i], maxItemBytes)
		if t {
			truncated = true
		}
		if val != "" {
			out = append(out, val)
		}
	}
	return out, truncated
}

func toProjectedNominationCard(card domain.MaintenanceNominationCard) domain.MaintenanceProjectedCard {
	return domain.MaintenanceProjectedCard{
		UnitID:              card.UnitID,
		UnitKind:            card.UnitKind,
		ProjectID:           card.ProjectID,
		VersionID:           card.VersionID,
		Title:               card.Title,
		Headings:            card.Headings,
		DeclaredTopics:      card.DeclaredTopics,
		SourcePaths:         card.SourcePaths,
		BodyExcerpt:         card.BodyExcerpt,
		ExcerptCoverage:     card.ExcerptCoverage,
		TotalBodyBytes:      card.TotalBodyBytes,
		AuthoredRefUnitIDs:  card.AuthoredRefUnitIDs,
		SharedSourceUnitIDs: card.SharedSourceUnitIDs,
	}
}

// encodedJSONTextBytes returns the exact byte count produced when raw JSON text is
// marshaled into a JSON string literal (including the 2 outer quotes).
func encodedJSONTextBytes(raw []byte) int {
	encoded, _ := json.Marshal(string(raw))
	return len(encoded)
}

func encodedJSONFragmentBytes(raw []byte) int {
	return encodedJSONTextBytes(raw) - 2
}

func shrinkNominationCardToLimits(card domain.MaintenanceNominationCard, maxRawBytes, maxEncodedBytes int) (domain.MaintenanceProjectedCard, domain.MaintenanceNominationCard, error) {
	return shrinkNominationCardWithOrigBody(card, card.BodyExcerpt, maxRawBytes, maxEncodedBytes)
}

func shrinkNominationCardWithOrigBody(card domain.MaintenanceNominationCard, origBody string, maxRawBytes, maxEncodedBytes int) (domain.MaintenanceProjectedCard, domain.MaintenanceNominationCard, error) {
	proj := toProjectedNominationCard(card)
	b, err := json.Marshal(proj)
	if err != nil {
		return domain.MaintenanceProjectedCard{}, domain.MaintenanceNominationCard{}, err
	}
	fits := func(raw []byte) bool {
		return len(raw) <= maxRawBytes && encodedJSONFragmentBytes(raw) <= maxEncodedBytes
	}
	if fits(b) {
		return proj, card, nil
	}

	card.ExcerptCoverage = "truncated"
	proj.ExcerptCoverage = "truncated"

	// Step 1: progressively trim BodyExcerpt from the unspliced original excerpt using UTF-8 head/tail windowing.
	curBodyLimit := len(card.BodyExcerpt)
	if len(origBody) < curBodyLimit {
		origBody = card.BodyExcerpt
	}
	for curBodyLimit > 0 {
		rawExcess := len(b) - maxRawBytes
		encExcess := encodedJSONFragmentBytes(b) - maxEncodedBytes
		excess := rawExcess
		if encExcess > excess {
			excess = encExcess
		}
		step := excess / 2
		if step < 16 {
			step = 16
		}
		nextLen := curBodyLimit - step
		if nextLen < 0 {
			nextLen = 0
		}
		card.BodyExcerpt, _ = truncateUTF8HeadTailBytes(origBody, nextLen)
		if len(card.BodyExcerpt) < nextLen {
			curBodyLimit = len(card.BodyExcerpt)
		} else {
			curBodyLimit = nextLen
		}
		proj = toProjectedNominationCard(card)
		b, err = json.Marshal(proj)
		if err != nil {
			return domain.MaintenanceProjectedCard{}, domain.MaintenanceNominationCard{}, err
		}
		if fits(b) {
			return proj, card, nil
		}
	}

	// Step 2: progressively drop tail display slices while preserving identity.
	dropStep := func() bool {
		if len(card.Headings) > 0 {
			card.Headings = card.Headings[:len(card.Headings)-1]
			return true
		}
		if len(card.DeclaredTopics) > 0 {
			card.DeclaredTopics = card.DeclaredTopics[:len(card.DeclaredTopics)-1]
			return true
		}
		if len(card.SourcePaths) > 0 {
			card.SourcePaths = card.SourcePaths[:len(card.SourcePaths)-1]
			return true
		}
		if len(card.SharedSourceUnitIDs) > 0 {
			card.SharedSourceUnitIDs = card.SharedSourceUnitIDs[:len(card.SharedSourceUnitIDs)-1]
			return true
		}
		if len(card.AuthoredRefUnitIDs) > 0 {
			card.AuthoredRefUnitIDs = card.AuthoredRefUnitIDs[:len(card.AuthoredRefUnitIDs)-1]
			return true
		}
		if len(card.Title) > 0 {
			next := len(card.Title) / 2
			card.Title, _ = truncateUTF8Bytes(card.Title, next)
			return true
		}
		return false
	}

	for !fits(b) && dropStep() {
		proj = toProjectedNominationCard(card)
		b, err = json.Marshal(proj)
		if err != nil {
			return domain.MaintenanceProjectedCard{}, domain.MaintenanceNominationCard{}, err
		}
	}
	if !fits(b) {
		return domain.MaintenanceProjectedCard{}, domain.MaintenanceNominationCard{}, ErrMaintenanceInvalid
	}
	return proj, card, nil
}

// ProjectNominationCard enforces per-field UTF-8 limits and guarantees both the raw
// serialized card JSON and its frame-quoted JSON fragment are <= 8192 bytes while
// preserving full service identity and original TotalBodyBytes.
func ProjectNominationCard(raw domain.MaintenanceNominationCard) (domain.MaintenanceProjectedCard, domain.MaintenanceNominationCard, error) {
	card := raw
	truncated := card.ExcerptCoverage == "truncated" || len(card.BodyExcerpt) < card.TotalBodyBytes

	var t bool
	card.Title, t = truncateUTF8Bytes(card.Title, 200)
	if t {
		truncated = true
	}
	card.Headings, t = truncateUTF8Slice(card.Headings, 8, 120)
	if t {
		truncated = true
	}
	card.DeclaredTopics, t = truncateUTF8Slice(card.DeclaredTopics, 12, 80)
	if t {
		truncated = true
	}
	card.SourcePaths, t = truncateUTF8Slice(card.SourcePaths, 12, 160)
	if t {
		truncated = true
	}
	card.AuthoredRefUnitIDs, t = truncateUTF8Slice(card.AuthoredRefUnitIDs, 8, 16)
	if t {
		truncated = true
	}
	card.SharedSourceUnitIDs, t = truncateUTF8Slice(card.SharedSourceUnitIDs, 8, 16)
	if t {
		truncated = true
	}
	if truncated {
		card.ExcerptCoverage = "truncated"
	} else {
		card.ExcerptCoverage = "full"
	}

	return shrinkNominationCardWithOrigBody(card, raw.BodyExcerpt, MaxNominationCardJSONBytes, MaxNominationCardJSONBytes)
}

func ComputeCardPayloadDigest(cards []domain.MaintenanceProjectedCard) (string, error) {
	raw, err := json.Marshal(cards)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func marshalNominationEnvelope(cards []domain.MaintenanceProjectedCard) ([]byte, error) {
	envelope := struct {
		Consumer string                            `json:"consumer"`
		Cards    []domain.MaintenanceProjectedCard `json:"cards"`
	}{
		Consumer: "managed-maintenance-nomination",
		Cards:    cards,
	}
	return json.Marshal(envelope)
}

func BuildNominationBatchPrompt(cards []domain.MaintenanceProjectedCard) (string, string, error) {
	if len(cards) == 0 || len(cards) > MaxNominationCardsPerBatch {
		return "", "", ErrMaintenanceInvalid
	}
	for i := range cards {
		rawCard, err := json.Marshal(cards[i])
		if err != nil || len(rawCard) > MaxNominationCardJSONBytes || encodedJSONFragmentBytes(rawCard) > MaxNominationCardJSONBytes {
			return "", "", ErrMaintenanceInvalid
		}
	}
	digest, err := ComputeCardPayloadDigest(cards)
	if err != nil {
		return "", "", err
	}
	promptBytes, err := marshalNominationEnvelope(cards)
	if err != nil {
		return "", "", err
	}
	if len(promptBytes)+NominationFixedFrameReserve > MaxNominationFrameBytes ||
		encodedJSONTextBytes(promptBytes)+NominationFixedFrameReserve > MaxNominationFrameBytes {
		return "", "", ErrMaintenanceInvalid
	}
	return string(promptBytes), digest, nil
}

// ProjectNominationBatchCards deterministically projects and shrinks a batch of <= 64
// nomination cards so the complete frame-encoded prompt + fixed reserve fits within
// 144 KiB BEFORE computing final stored cards, projected cards, and CardPayloadDigest.
func ProjectNominationBatchCards(rawCards []domain.MaintenanceNominationCard) ([]domain.MaintenanceNominationCard, []domain.MaintenanceProjectedCard, string, string, error) {
	if len(rawCards) == 0 || len(rawCards) > MaxNominationCardsPerBatch {
		return nil, nil, "", "", ErrMaintenanceInvalid
	}
	stored := make([]domain.MaintenanceNominationCard, len(rawCards))
	projected := make([]domain.MaintenanceProjectedCard, len(rawCards))
	for i := range rawCards {
		proj, st, err := ProjectNominationCard(rawCards[i])
		if err != nil {
			return nil, nil, "", "", err
		}
		stored[i] = st
		projected[i] = proj
	}

	maxAllowedEncodedPrompt := MaxNominationFrameBytes - NominationFixedFrameReserve
	for {
		promptBytes, err := marshalNominationEnvelope(projected)
		if err != nil {
			return nil, nil, "", "", err
		}
		if len(promptBytes) <= maxAllowedEncodedPrompt && encodedJSONTextBytes(promptBytes) <= maxAllowedEncodedPrompt {
			break
		}
		maxIdx := -1
		maxEnc := 0
		for i := range projected {
			cb, err := json.Marshal(projected[i])
			if err != nil {
				return nil, nil, "", "", err
			}
			enc := encodedJSONFragmentBytes(cb)
			if enc > maxEnc {
				maxEnc = enc
				maxIdx = i
			}
		}
		if maxIdx < 0 || maxEnc <= 160 {
			return nil, nil, "", "", ErrMaintenanceInvalid
		}
		targetEnc := maxEnc - 64
		if targetEnc < 160 {
			targetEnc = 160
		}
		newProj, newStored, err := shrinkNominationCardWithOrigBody(stored[maxIdx], rawCards[maxIdx].BodyExcerpt, MaxNominationCardJSONBytes, targetEnc)
		if err != nil {
			return nil, nil, "", "", err
		}
		nb, _ := json.Marshal(newProj)
		if encodedJSONFragmentBytes(nb) >= maxEnc {
			return nil, nil, "", "", ErrMaintenanceInvalid
		}
		stored[maxIdx] = newStored
		projected[maxIdx] = newProj
	}

	promptStr, digest, err := BuildNominationBatchPrompt(projected)
	if err != nil {
		return nil, nil, "", "", err
	}
	return stored, projected, promptStr, digest, nil
}

// PlanNominationBatches deterministically plans <= 1 batch when N <= 64 and
// <= 2*ceil(N/64) two-pass cross-project interleaved batches when N > 64.
func PlanNominationBatches(items []domain.JobItem) []plannedNominationBatch {
	sorted := make([]domain.JobItem, len(items))
	copy(sorted, items)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].ProjectID != sorted[j].ProjectID {
			return sorted[i].ProjectID < sorted[j].ProjectID
		}
		if sorted[i].VersionID != sorted[j].VersionID {
			return sorted[i].VersionID < sorted[j].VersionID
		}
		if sorted[i].PagePath != sorted[j].PagePath {
			return sorted[i].PagePath < sorted[j].PagePath
		}
		if sorted[i].RegionID != sorted[j].RegionID {
			return sorted[i].RegionID < sorted[j].RegionID
		}
		return sorted[i].ItemID < sorted[j].ItemID
	})

	n := len(sorted)
	if n < 2 {
		return nil
	}
	if n <= MaxNominationCardsPerBatch {
		return []plannedNominationBatch{
			{
				BatchID: "nomination:p0:b0000",
				Pass:    0,
				Items:   sorted,
			},
		}
	}

	numBatches := (n + MaxNominationCardsPerBatch - 1) / MaxNominationCardsPerBatch
	pass0 := make([][]domain.JobItem, numBatches)
	for idx, it := range sorted {
		b := idx % numBatches
		pass0[b] = append(pass0[b], it)
	}

	var out []plannedNominationBatch
	for b := 0; b < numBatches; b++ {
		if len(pass0[b]) >= 2 {
			out = append(out, plannedNominationBatch{
				BatchID: fmt.Sprintf("nomination:p0:b%04d", b),
				Pass:    0,
				Items:   pass0[b],
			})
		}
	}

	// Pass 1 permutes each Pass-0 coordinate (b, slot) -> b' = (b + slot + 1) % numBatches
	// so units from distinct Pass-0 windows co-occur in <= 2*ceil(N/64) total batches.
	pass1 := make([][]domain.JobItem, numBatches)
	maxSlots := 0
	for b := 0; b < numBatches; b++ {
		if len(pass0[b]) > maxSlots {
			maxSlots = len(pass0[b])
		}
	}
	for slot := 0; slot < maxSlots; slot++ {
		for b := 0; b < numBatches; b++ {
			if slot >= len(pass0[b]) {
				continue
			}
			targetB := (b + slot + 1) % numBatches
			if len(pass1[targetB]) >= MaxNominationCardsPerBatch {
				// Find first batch with remaining capacity.
				for off := 1; off < numBatches; off++ {
					alt := (targetB + off) % numBatches
					if len(pass1[alt]) < MaxNominationCardsPerBatch {
						targetB = alt
						break
					}
				}
			}
			pass1[targetB] = append(pass1[targetB], pass0[b][slot])
		}
	}
	for b := 0; b < numBatches; b++ {
		if len(pass1[b]) >= 2 {
			out = append(out, plannedNominationBatch{
				BatchID: fmt.Sprintf("nomination:p1:b%04d", b),
				Pass:    1,
				Items:   pass1[b],
			})
		}
	}
	return out
}

func decodeMaintenanceNominationOutput(
	raw []byte,
	cards []domain.MaintenanceNominationCard,
) (domain.MaintenanceNominationEnvelope, map[string]domain.MaintenanceNominationFocalResult, error) {
	var env domain.MaintenanceNominationEnvelope
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || len(cards) == 0 || len(cards) > MaxNominationCardsPerBatch {
		return env, nil, ErrMaintenanceInvalid
	}

	var topMap map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &topMap); err != nil {
		return env, nil, ErrMaintenanceInvalid
	}
	if _, hasNominations := topMap["nominations"]; !hasNominations || len(topMap) != 1 {
		return env, nil, ErrMaintenanceInvalid
	}

	type strictCandidate struct {
		CandidateUnitID *string `json:"candidate_unit_id"`
		Rationale       *string `json:"rationale"`
	}
	type strictFocalResult struct {
		FocalUnitID   *string           `json:"focal_unit_id"`
		Abstained     *bool             `json:"abstained"`
		AbstainReason *string           `json:"abstain_reason,omitempty"`
		Candidates    []strictCandidate `json:"candidates"`
	}
	type strictEnvelope struct {
		Nominations []strictFocalResult `json:"nominations"`
	}

	var sEnv strictEnvelope
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&sEnv); err != nil {
		return env, nil, ErrMaintenanceInvalid
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return env, nil, ErrMaintenanceInvalid
	}
	if sEnv.Nominations == nil || len(sEnv.Nominations) == 0 || len(sEnv.Nominations) != len(cards) {
		return env, nil, ErrMaintenanceInvalid
	}

	cardByUnitID := make(map[string]domain.MaintenanceNominationCard, len(cards))
	for _, c := range cards {
		if c.UnitID == "" || c.ItemID == "" {
			return env, nil, ErrMaintenanceInvalid
		}
		cardByUnitID[c.UnitID] = c
	}

	seenFocal := make(map[string]bool, len(sEnv.Nominations))
	resolvedByItem := make(map[string]domain.MaintenanceNominationFocalResult, len(sEnv.Nominations))
	env.Nominations = make([]domain.MaintenanceNominationFocalResult, 0, len(sEnv.Nominations))

	for i := range sEnv.Nominations {
		nom := sEnv.Nominations[i]
		if nom.FocalUnitID == nil || nom.Abstained == nil || nom.Candidates == nil {
			return env, nil, ErrMaintenanceInvalid
		}
		focalUID := strings.TrimSpace(*nom.FocalUnitID)
		focalCard, ok := cardByUnitID[focalUID]
		if !ok || seenFocal[focalUID] {
			return env, nil, ErrMaintenanceInvalid
		}
		seenFocal[focalUID] = true
		if len(nom.Candidates) > maxShortlistCandidates {
			return env, nil, ErrMaintenanceInvalid
		}
		abstained := *nom.Abstained
		abstainReason := ""
		if nom.AbstainReason != nil {
			abstainReason = strings.TrimSpace(*nom.AbstainReason)
		}
		if abstained {
			if len(nom.Candidates) > 0 || abstainReason == "" {
				return env, nil, ErrMaintenanceInvalid
			}
		} else {
			if len(nom.Candidates) == 0 || abstainReason != "" {
				return env, nil, ErrMaintenanceInvalid
			}
		}

		seenCand := make(map[string]bool, len(nom.Candidates))
		rawCands := make([]domain.MaintenanceNominatedCandidate, 0, len(nom.Candidates))
		resolvedCands := make([]domain.MaintenanceNominatedCandidate, 0, len(nom.Candidates))
		for _, c := range nom.Candidates {
			if c.CandidateUnitID == nil || c.Rationale == nil {
				return env, nil, ErrMaintenanceInvalid
			}
			candUID := strings.TrimSpace(*c.CandidateUnitID)
			candCard, cOK := cardByUnitID[candUID]
			if !cOK {
				return env, nil, ErrMaintenanceInvalid
			}
			if candUID == focalUID || candCard.ItemID == focalCard.ItemID {
				return env, nil, ErrMaintenanceInvalid
			}
			if seenCand[candUID] {
				return env, nil, ErrMaintenanceInvalid
			}
			seenCand[candUID] = true
			rat := strings.TrimSpace(*c.Rationale)
			if rat == "" {
				return env, nil, ErrMaintenanceInvalid
			}
			rawCands = append(rawCands, domain.MaintenanceNominatedCandidate{
				CandidateUnitID: candUID,
				Rationale:       rat,
			})
			resolvedCands = append(resolvedCands, domain.MaintenanceNominatedCandidate{
				CandidateUnitID: candCard.ItemID,
				Rationale:       rat,
			})
		}

		env.Nominations = append(env.Nominations, domain.MaintenanceNominationFocalResult{
			FocalUnitID:   focalUID,
			Abstained:     abstained,
			AbstainReason: abstainReason,
			Candidates:    rawCands,
		})
		resolvedByItem[focalCard.ItemID] = domain.MaintenanceNominationFocalResult{
			FocalUnitID:   focalCard.ItemID,
			Abstained:     abstained,
			AbstainReason: abstainReason,
			Candidates:    resolvedCands,
		}
	}
	if len(seenFocal) != len(cards) {
		return env, nil, ErrMaintenanceInvalid
	}

	return env, resolvedByItem, nil
}

func (w *Worker) buildBatchNominationCards(
	tx app.WriteTx,
	run domain.ScanRun,
	items []domain.JobItem,
) ([]domain.MaintenanceNominationCard, []domain.MaintenanceProjectedCard, []domain.MaintenanceExplanation, string, string, error) {
	if len(items) < 2 || len(items) > MaxNominationCardsPerBatch {
		return nil, nil, nil, "", "", ErrMaintenanceInvalid
	}

	unitIDByKey := make(map[string]string, len(items))
	unitIDByVer := make(map[string][]string, len(items))
	unitIDByVerPage := make(map[string][]string, len(items))
	sigs := make([]unitSignals, len(items))
	exps := make([]domain.MaintenanceExplanation, len(items))
	cvs := make([]domain.ScanCapturedVersion, len(items))
	docs := make([]domain.Document, len(items))
	rawBodies := make([]string, len(items))

	for i, it := range items {
		uid := fmt.Sprintf("u%02d", i+1)
		cv, doc, _, ok := w.checkCandidateTargetFence(tx, run, it.VersionID, "codex")
		if !ok {
			return nil, nil, nil, "", "", domain.ErrDenied
		}
		sig, err := extractScopeThemes(tx, cv, it.PagePath, it.RegionID)
		if err != nil {
			return nil, nil, nil, "", "", err
		}
		exp, expOK := w.resolveUnitExplanation(tx, cv, doc.ID, it.PagePath, it.RegionID, "card", sig.Title)
		if !expOK {
			return nil, nil, nil, "", "", domain.ErrNotFound
		}
		exp.UnitID = uid
		exp.UnitKind = it.UnitKind
		exp.ItemID = it.ItemID

		_, _, _, sBody, _ := tx.ScanSearchText(it.VersionID, it.PagePath, it.RegionID)
		if strings.TrimSpace(sBody) == "" {
			sBody = sig.Excerpt
		}

		cvs[i] = cv
		docs[i] = doc
		sigs[i] = sig
		exps[i] = exp
		rawBodies[i] = strings.TrimSpace(sBody)

		uKey := candidateUnitKeyString(it.VersionID, it.PagePath, it.RegionID)
		unitIDByKey[uKey] = uid
		unitIDByVer[it.VersionID] = append(unitIDByVer[it.VersionID], uid)
		unitIDByVerPage[it.VersionID+"\x00"+it.PagePath] = append(unitIDByVerPage[it.VersionID+"\x00"+it.PagePath], uid)
	}

	rawCards := make([]domain.MaintenanceNominationCard, len(items))

	for i, it := range items {
		uid := fmt.Sprintf("u%02d", i+1)
		sig := sigs[i]

		var srcPaths []string
		for sp := range sig.SourcePaths {
			srcPaths = append(srcPaths, sp)
		}
		sort.Strings(srcPaths)

		var authoredUIDs []string
		seenRefUID := map[string]bool{}
		for _, ref := range sig.OutgoingRefs {
			var refPage, refReg string
			if ref.TargetPagePath != nil {
				refPage = *ref.TargetPagePath
			}
			if ref.TargetRegionID != nil {
				refReg = *ref.TargetRegionID
			}
			var matched []string
			if uKey := candidateUnitKeyString(ref.TargetVersionID, refPage, refReg); unitIDByKey[uKey] != "" {
				matched = append(matched, unitIDByKey[uKey])
			} else if refReg == "" && refPage != "" {
				matched = append(matched, unitIDByVerPage[ref.TargetVersionID+"\x00"+refPage]...)
			} else if refPage == "" && refReg == "" {
				matched = append(matched, unitIDByVer[ref.TargetVersionID]...)
			}
			for _, mUID := range matched {
				if mUID != "" && mUID != uid && !seenRefUID[mUID] {
					seenRefUID[mUID] = true
					authoredUIDs = append(authoredUIDs, mUID)
				}
			}
		}
		sort.Strings(authoredUIDs)

		var sharedSourceUIDs []string
		if len(sig.SourcePaths) > 0 {
			for j := range items {
				if i == j {
					continue
				}
				otherUID := fmt.Sprintf("u%02d", j+1)
				for sp := range sig.SourcePaths {
					if sigs[j].SourcePaths[sp] {
						sharedSourceUIDs = append(sharedSourceUIDs, otherUID)
						break
					}
				}
			}
			sort.Strings(sharedSourceUIDs)
		}

		bodyText := rawBodies[i]
		totalBodyBytes := len(bodyText)
		rawCards[i] = domain.MaintenanceNominationCard{
			UnitID:              uid,
			ItemID:              it.ItemID,
			UnitKind:            it.UnitKind,
			ProjectID:           it.ProjectID,
			DocumentID:          docs[i].ID,
			VersionID:           it.VersionID,
			PagePath:            it.PagePath,
			RegionID:            it.RegionID,
			TargetSetID:         cvs[i].TargetSetID,
			ObjectHash:          exps[i].ObjectHash,
			ByteSize:            exps[i].Size,
			Title:               sig.Title,
			Headings:            sig.Headings,
			DeclaredTopics:      sig.Themes,
			SourcePaths:         srcPaths,
			BodyExcerpt:         bodyText,
			ExcerptCoverage:     "full",
			TotalBodyBytes:      totalBodyBytes,
			AuthoredRefUnitIDs:  authoredUIDs,
			SharedSourceUnitIDs: sharedSourceUIDs,
		}
	}

	cards, projected, promptStr, digest, err := ProjectNominationBatchCards(rawCards)
	if err != nil {
		return nil, nil, nil, "", "", err
	}
	return cards, projected, exps, promptStr, digest, nil
}

func isStructuralReadyItem(item domain.JobItem) bool {
	if len(item.Detail) == 0 {
		return false
	}
	var d struct {
		StructuralReady bool `json:"structural_ready"`
	}
	if json.Unmarshal(item.Detail, &d) != nil {
		return false
	}
	return d.StructuralReady
}

func (w *Worker) collectEligibleSemanticItems(
	tx app.WriteTx,
	j domain.Job,
	run domain.ScanRun,
	cfgJSON []byte,
) ([]domain.JobItem, map[string]domain.JobItem, bool, error) {
	if !run.EnumerationComplete {
		return nil, nil, false, nil
	}
	var allItems []domain.JobItem
	afterOrdinal := 0
	for {
		page, err := tx.JobItems(j.ID, afterOrdinal, 128)
		if err != nil {
			return nil, nil, false, err
		}
		if len(page) == 0 {
			break
		}
		allItems = append(allItems, page...)
		afterOrdinal = page[len(page)-1].Ordinal
		if len(page) < 128 {
			break
		}
	}

	var eligible []domain.JobItem
	byID := make(map[string]domain.JobItem, len(allItems))
	allStructuralReady := true
	for _, it := range allItems {
		if (it.State == "pending" || it.State == "running") && !isStructuralReadyItem(it) {
			allStructuralReady = false
		}
		if it.UnitKind != "version" && it.UnitKind != "page" && it.UnitKind != "region" {
			continue
		}
		if !isStructuralReadyItem(it) {
			continue
		}
		if it.State != "pending" && it.State != "running" && it.State != "completed" {
			continue
		}
		if _, _, _, fenced := w.checkItemFence(tx, j, run, it); fenced {
			continue
		}
		if !projectHasProviderID(cfgJSON, it.ProjectID, "codex") {
			continue
		}
		cv, doc, _, ok := w.checkCandidateTargetFence(tx, run, it.VersionID, "codex")
		if !ok {
			continue
		}
		if _, expOK := w.resolveUnitExplanation(tx, cv, doc.ID, it.PagePath, it.RegionID, "card", ""); !expOK {
			continue
		}
		byID[it.ItemID] = it
		if it.Stage == "inspect" && (it.State == "pending" || it.State == "running") {
			eligible = append(eligible, it)
		}
	}
	return eligible, byID, allStructuralReady, nil
}

func (w *Worker) stepGlobalNominationPrephase(
	tx app.WriteTx,
	j domain.Job,
	run *domain.ScanRun,
	cfgJSON []byte,
	maxAttempts int,
	nowStr string,
) (globalNominationStatus, error) {
	status := globalNominationStatus{
		ByItemID: map[string]aggregatedUnitNomination{},
		ItemByID: map[string]domain.JobItem{},
	}

	eligible, byID, allStructuralReady, err := w.collectEligibleSemanticItems(tx, j, *run, cfgJSON)
	if err != nil {
		return status, err
	}
	status.ItemByID = byID
	status.EligibleUnitCount = len(eligible)
	if !allStructuralReady {
		return status, nil
	}

	allIntents, err := tx.MaintenanceIntentsByScan(j.ID)
	if err != nil {
		return status, err
	}
	nomIntentsByBatch := map[string]domain.MaintenanceIntent{}
	var existingBatchOrder []string
	for _, m := range allIntents {
		if m.Mode == domain.MaintenanceModeSemanticNomination {
			bid := m.BatchID
			if bid == "" {
				bid = m.ItemID
			}
			if bid != "" {
				if _, seen := nomIntentsByBatch[bid]; !seen {
					existingBatchOrder = append(existingBatchOrder, bid)
				}
				nomIntentsByBatch[bid] = m
			}
		}
	}
	sort.Strings(existingBatchOrder)

	var planned []plannedNominationBatch
	if len(byID) == len(eligible) {
		planned = PlanNominationBatches(eligible)
	}
	if len(nomIntentsByBatch) == 0 && len(planned) == 0 {
		status.Settled = true
		status.Coverage = "single_batch"
		return status, nil
	}

	// Build ordered batch descriptors: preserve any already-created batch IDs first,
	// plus any remaining planned batches in deterministic order.
	plannedByID := map[string]plannedNominationBatch{}
	var orderedBatchIDs []string
	seenBatchID := map[string]bool{}
	for _, b := range planned {
		plannedByID[b.BatchID] = b
		if !seenBatchID[b.BatchID] {
			seenBatchID[b.BatchID] = true
			orderedBatchIDs = append(orderedBatchIDs, b.BatchID)
		}
	}
	for _, bid := range existingBatchOrder {
		if !seenBatchID[bid] {
			seenBatchID[bid] = true
			orderedBatchIDs = append(orderedBatchIDs, bid)
		}
	}
	sort.Strings(orderedBatchIDs)
	// If existing batches recorded a smaller TotalPlannedBatches (e.g., single_batch=1)
	// or if a prior batch already stopped on budget/invalidation, cap orderedBatchIDs.
	for _, m := range nomIntentsByBatch {
		if m.TotalPlannedBatches > 0 && len(orderedBatchIDs) > m.TotalPlannedBatches && len(existingBatchOrder) <= m.TotalPlannedBatches {
			orderedBatchIDs = orderedBatchIDs[:m.TotalPlannedBatches]
			break
		}
	}
	totalPlanned := len(orderedBatchIDs)
	status.TotalPlanned = totalPlanned

	partialLifecycle := false
	partialBudget := false

	for _, bid := range orderedBatchIDs {
		if intent, exists := nomIntentsByBatch[bid]; exists {
			if intent.TotalPlannedBatches > totalPlanned {
				totalPlanned = intent.TotalPlannedBatches
				status.TotalPlanned = totalPlanned
			}
			if intent.Purged || intent.NominationState == "invalidated" || intent.NominationState == "skipped" {
				partialLifecycle = true
				continue
			}

			child, cErr := tx.Job(intent.JobID)
			if cErr != nil {
				return status, cErr
			}
			childTerminal := child.State == "completed" || child.State == "failed" || child.State == "cancelled"

			// Check if any covered card in this batch is now fenced.
			cardFenced := false
			for _, card := range intent.Cards {
				it, itErr := tx.JobItem(j.ID, card.ItemID)
				if itErr != nil {
					cardFenced = true
					break
				}
				if _, _, _, fenced := w.checkItemFence(tx, j, *run, it); fenced {
					cardFenced = true
					break
				}
				if _, _, _, ok := w.checkCandidateTargetFence(tx, *run, card.VersionID, "codex"); !ok {
					cardFenced = true
					break
				}
			}

			if cardFenced {
				partialLifecycle = true
				if !childTerminal {
					if err := w.cancelSingleRepairChild(tx, child.ID, "batch_card_fenced", nowStr); err != nil {
						return status, err
					}
					child, cErr = tx.Job(intent.JobID)
					if cErr != nil {
						return status, cErr
					}
					childTerminal = child.State == "completed" || child.State == "failed" || child.State == "cancelled"
				}
				if child.ProviderAttempts != nil && (child.ProviderAttempts.Unknown > 0 || child.ProviderAttempts.LatestState == "result_saved") && !childTerminal {
					reason := child.Error
					if reason == "" {
						reason = "outcome_unknown"
					}
					if err := tx.PauseProviderJob(j.ID, reason); err != nil {
						return status, err
					}
					return status, nil
				}
				if !childTerminal {
					return status, nil
				}
				intent.NominationState = "invalidated"
				intent.NominationReason = "batch_card_fenced"
				intent.PreparedOutputJSON = nil
				if err := tx.UpdateMaintenanceIntent(intent); err != nil {
					return status, err
				}
				continue
			}

			switch child.State {
			case "completed":
				if intent.NominationState != "completed" {
					intent.NominationState = "completed"
					if err := tx.UpdateMaintenanceIntent(intent); err != nil {
						return status, err
					}
				}
				if len(intent.PreparedOutputJSON) > 0 {
					var prep domain.PreparedMaintenanceNominationPayload
					if json.Unmarshal(intent.PreparedOutputJSON, &prep) == nil {
						status.VisitedBatchIDs = append(status.VisitedBatchIDs, bid)
						for itemID, res := range prep.Shortlists {
							agg := status.ByItemID[itemID]
							agg.HasCompletedResult = true
							agg.VisitedBatchIDs = append(agg.VisitedBatchIDs, bid)
							if len(res.Candidates) > 0 {
								agg.Abstained = false
								agg.AbstainReason = ""
								agg.Candidates = append(agg.Candidates, res.Candidates...)
							} else if res.Abstained && len(agg.Candidates) == 0 {
								agg.Abstained = true
								if agg.AbstainReason == "" {
									agg.AbstainReason = res.AbstainReason
								}
							}
							status.ByItemID[itemID] = agg
						}
					}
				}
				continue

			case "queued", "running", "reconciling", "cancelling":
				return status, nil

			case "paused":
				if child.ProviderAttempts != nil && (child.ProviderAttempts.Unknown > 0 || child.ProviderAttempts.LatestState == "result_saved") {
					reason := child.Error
					if reason == "" {
						reason = "outcome_unknown"
					}
					if err := tx.PauseProviderJob(j.ID, reason); err != nil {
						return status, err
					}
					return status, nil
				}
				switch child.Error {
				case "outcome_unknown", "saved_result_requires_continuation", "reconciliation_required", "authority_reconciliation_required", "preparation_failed", "captured_input_unavailable", "captured_input_invalid":
					if err := tx.PauseProviderJob(j.ID, child.Error); err != nil {
						return status, err
					}
					return status, nil
				default:
					cancelReason := child.Error
					if cancelReason == "" {
						cancelReason = "paused_child_cancelled"
					}
					if err := w.cancelSingleRepairChild(tx, child.ID, cancelReason, nowStr); err != nil {
						return status, err
					}
					child, cErr = tx.Job(intent.JobID)
					if cErr != nil {
						return status, cErr
					}
					childTerminal = child.State == "completed" || child.State == "failed" || child.State == "cancelled"
					if child.ProviderAttempts != nil && (child.ProviderAttempts.Unknown > 0 || child.ProviderAttempts.LatestState == "result_saved") && !childTerminal {
						reason := child.Error
						if reason == "" {
							reason = "outcome_unknown"
						}
						if err := tx.PauseProviderJob(j.ID, reason); err != nil {
							return status, err
						}
						return status, nil
					}
					if !childTerminal {
						return status, nil
					}
					intent.NominationState = "invalidated"
					intent.NominationReason = cancelReason
					intent.PreparedOutputJSON = nil
					if err := tx.UpdateMaintenanceIntent(intent); err != nil {
						return status, err
					}
					partialLifecycle = true
					continue
				}

			default: // failed or cancelled
				if child.ProviderAttempts != nil && (child.ProviderAttempts.Unknown > 0 || child.ProviderAttempts.LatestState == "result_saved") {
					reason := child.Error
					if reason == "" {
						reason = "outcome_unknown"
					}
					if err := tx.PauseProviderJob(j.ID, reason); err != nil {
						return status, err
					}
					return status, nil
				}
				intent.NominationState = "invalidated"
				intent.NominationReason = child.Error
				intent.PreparedOutputJSON = nil
				if err := tx.UpdateMaintenanceIntent(intent); err != nil {
					return status, err
				}
				partialLifecycle = true
				continue
			}
		}

		// Batch intent does not exist yet; create the next planned nomination child job.
		plan, hasPlan := plannedByID[bid]
		if !hasPlan || len(plan.Items) < 2 {
			partialLifecycle = true
			continue
		}

		usedAttempts, err := tx.ScanProviderAttemptCount(j.ID, "")
		if err != nil {
			return status, err
		}
		if maxAttempts > 0 && usedAttempts >= maxAttempts {
			run.Report.BudgetExhausted = true
			if err := tx.UpdateScanRun(*run); err != nil {
				return status, err
			}
			partialBudget = true
			break
		}

		cards, _, exps, promptStr, cardDigest, err := w.buildBatchNominationCards(tx, *run, plan.Items)
		if err != nil {
			partialLifecycle = true
			continue
		}

		childID, err := service.NewID()
		if err != nil {
			return status, err
		}
		batchItemIDs := make([]string, len(cards))
		for i, c := range cards {
			batchItemIDs[i] = c.ItemID
		}

		firstCard := cards[0]
		var pagePtr, regPtr *string
		if firstCard.PagePath != "" {
			p := firstCard.PagePath
			pagePtr = &p
		}
		if firstCard.RegionID != "" {
			r := firstCard.RegionID
			regPtr = &r
		}
		firstCV, _, _, _ := w.checkCandidateTargetFence(tx, *run, firstCard.VersionID, "codex")

		maintInv := &domain.MaintenanceInvocation{
			Mode:              domain.MaintenanceModeSemanticNomination,
			ScanJobID:         j.ID,
			ItemID:            bid,
			BatchID:           bid,
			BatchItemIDs:      batchItemIDs,
			CardPayloadDigest: cardDigest,
			UnitKind:          firstCard.UnitKind,
			ProjectID:         firstCard.ProjectID,
			DocumentID:        firstCard.DocumentID,
			VersionID:         firstCard.VersionID,
			PagePath:          pagePtr,
			RegionID:          regPtr,
			SourceMode:        firstCV.SelectedMode,
			Explanations:      exps,
		}
		inv := domain.ProviderInvocation{
			Prompt:      promptStr,
			Maintenance: maintInv,
		}
		docIDCopy := firstCard.DocumentID
		targetBytes, _ := json.Marshal(map[string]any{
			"action":      "maintenance_nomination",
			"project_id":  firstCard.ProjectID,
			"document_id": &docIDCopy,
			"version_id":  firstCard.VersionID,
			"page_path":   pagePtr,
			"region_id":   regPtr,
			"scan_job_id": j.ID,
			"item_id":     bid,
			"batch_id":    bid,
		})

		reqSHA, err := w.Identity(run.PrincipalID, targetBytes, inv, nil)
		if err != nil {
			return status, err
		}
		authState, err := tx.AuthState()
		if err != nil {
			return status, err
		}
		credScope := run.CapturedScope
		if run.AuthKind == "owner" {
			credScope = domain.CredentialScope{Kind: "owner"}
		}

		pScope := domain.ProviderScope{
			Purpose:         "body",
			PrincipalID:     run.PrincipalID,
			AuthEpoch:       authState.AuthEpoch,
			CredentialScope: credScope,
			ProviderID:      "codex",
			RequestSHA:      reqSHA,
			Target:          targetBytes,
			Invocation:      &inv,
		}
		scopeBytes, _ := json.Marshal(pScope)

		perms := map[string]int64{}
		for _, exp := range exps {
			if rev, ok := run.PermissionsRevisions[exp.ProjectID]; ok {
				perms[exp.ProjectID] = rev
			}
		}

		coverageKind := "single_batch"
		if totalPlanned > 1 {
			coverageKind = "bounded_window_scope"
		}
		intent := domain.MaintenanceIntent{
			JobID:               childID,
			ScanJobID:           j.ID,
			ItemID:              bid,
			Mode:                domain.MaintenanceModeSemanticNomination,
			BatchID:             bid,
			BatchPass:           plan.Pass,
			TotalPlannedBatches: totalPlanned,
			BatchItemIDs:        batchItemIDs,
			Cards:               cards,
			CardPayloadDigest:   cardDigest,
			NominationState:     "pending",
			NominationReason:    coverageKind,
			UnitKind:            firstCard.UnitKind,
			ProjectID:           firstCard.ProjectID,
			DocumentID:          firstCard.DocumentID,
			VersionID:           firstCard.VersionID,
			PagePath:            firstCard.PagePath,
			RegionID:            firstCard.RegionID,
			SourceMode:          firstCV.SelectedMode,
			AdmissionReady:      true,
		}
		if err := tx.CreateMaintenanceJob(domain.Job{
			ID:             childID,
			Kind:           "maintenance",
			ParentID:       j.ID,
			State:          "queued",
			Scope:          scopeBytes,
			ConfigRevision: run.ConfigRevision,
			Permissions:    perms,
		}, intent); err != nil {
			return status, err
		}
		return status, nil
	}

	if len(existingBatchOrder) < totalPlanned && !partialBudget && !partialLifecycle {
		if run.Report.BudgetExhausted {
			partialBudget = true
		} else {
			partialLifecycle = true
		}
	}
	status.Settled = true
	switch {
	case partialBudget:
		status.Coverage = "partial_budget"
	case partialLifecycle:
		status.Coverage = "partial_lifecycle"
	case totalPlanned > 1:
		status.Coverage = "bounded_window_scope"
	default:
		status.Coverage = "single_batch"
	}
	return status, nil
}

func (w *Worker) mergeNominatedShortlist(
	tx app.WriteTx,
	run domain.ScanRun,
	focalItem domain.JobItem,
	baseShortlist domain.CandidateShortlist,
	nomStatus globalNominationStatus,
) domain.CandidateShortlist {
	agg, hasNom := nomStatus.ByItemID[focalItem.ItemID]
	if !hasNom || len(agg.Candidates) == 0 {
		return baseShortlist
	}

	baseByUnit := map[string]domain.CandidateTarget{}
	var structuralAnchors []domain.CandidateTarget
	var lexicalFallbacks []domain.CandidateTarget
	for _, cand := range baseShortlist.Candidates {
		uKey := candidateUnitKeyString(cand.VersionID, cand.PagePath, cand.RegionID)
		baseByUnit[uKey] = cand
		isAnchor := false
		for _, sig := range cand.Signals {
			if sig.Kind == "authored_reference" || sig.Kind == "source_path_overlap" {
				isAnchor = true
				break
			}
		}
		if isAnchor {
			structuralAnchors = append(structuralAnchors, cand)
		} else {
			lexicalFallbacks = append(lexicalFallbacks, cand)
		}
	}

	selected := make([]domain.CandidateTarget, 0, maxShortlistCandidates)
	seenUnit := map[string]bool{}

	// 1. Structural anchors (authored_reference, source_path_overlap) first.
	for _, anc := range structuralAnchors {
		uKey := candidateUnitKeyString(anc.VersionID, anc.PagePath, anc.RegionID)
		if seenUnit[uKey] {
			continue
		}
		for _, nc := range agg.Candidates {
			if candIt, ok := nomStatus.ItemByID[nc.CandidateUnitID]; ok {
				if candidateUnitKeyString(candIt.VersionID, candIt.PagePath, candIt.RegionID) == uKey {
					anc.Signals = append(anc.Signals, domain.CandidateSignal{
						Kind:   "semantic_nomination",
						Weight: 5.5,
						Detail: formatUnverifiedNominationHint(nc.Rationale),
					})
					anc.Score += 5.5
					break
				}
			}
		}
		seenUnit[uKey] = true
		selected = append(selected, anc)
		if len(selected) >= maxShortlistCandidates {
			break
		}
	}

	// 2. Validated model-nominated candidates from completed valid nomination batches.
	for idx, nc := range agg.Candidates {
		if len(selected) >= maxShortlistCandidates {
			break
		}
		candItem, ok := nomStatus.ItemByID[nc.CandidateUnitID]
		if !ok {
			continue
		}
		if candItem.VersionID == focalItem.VersionID && candItem.PagePath == focalItem.PagePath && candItem.RegionID == focalItem.RegionID {
			continue
		}
		uKey := candidateUnitKeyString(candItem.VersionID, candItem.PagePath, candItem.RegionID)
		if seenUnit[uKey] {
			continue
		}
		cv, doc, _, ok := w.checkCandidateTargetFence(tx, run, candItem.VersionID, "codex")
		if !ok {
			continue
		}
		rankBoost := float64(maxShortlistCandidates-idx) * 0.1
		if existing, exists := baseByUnit[uKey]; exists {
			existing.Signals = append([]domain.CandidateSignal{
				{
					Kind:   "semantic_nomination",
					Weight: 5.5,
					Detail: formatUnverifiedNominationHint(nc.Rationale),
				},
			}, existing.Signals...)
			existing.Score += 5.5 + rankBoost
			seenUnit[uKey] = true
			selected = append(selected, existing)
			continue
		}

		candSig, err := extractScopeThemes(tx, cv, candItem.PagePath, candItem.RegionID)
		if err != nil {
			continue
		}
		seenUnit[uKey] = true
		selected = append(selected, domain.CandidateTarget{
			ProjectID:   cv.ProjectID,
			DocumentID:  doc.ID,
			VersionID:   cv.VersionID,
			PagePath:    candItem.PagePath,
			RegionID:    candItem.RegionID,
			UnitKind:    candItem.UnitKind,
			TargetSetID: cv.TargetSetID,
			Title:       candSig.Title,
			Score:       5.5 + rankBoost,
			Signals: []domain.CandidateSignal{
				{
					Kind:   "semantic_nomination",
					Weight: 5.5,
					Detail: formatUnverifiedNominationHint(nc.Rationale),
				},
			},
		})
	}

	// 3. Deterministic lexical candidates only for remaining slots up to maxShortlistCandidates.
	for _, lex := range lexicalFallbacks {
		if len(selected) >= maxShortlistCandidates {
			break
		}
		uKey := candidateUnitKeyString(lex.VersionID, lex.PagePath, lex.RegionID)
		if seenUnit[uKey] {
			continue
		}
		seenUnit[uKey] = true
		selected = append(selected, lex)
	}

	out := baseShortlist
	out.Candidates = selected
	fpCandidates := make([]domain.CandidateTarget, len(selected))
	copy(fpCandidates, selected)
	for i := range fpCandidates {
		fpCandidates[i].TargetSetID = ""
	}
	fpBytes, _ := json.Marshal(struct {
		VersionID  string                   `json:"version_id"`
		PagePath   string                   `json:"page_path"`
		RegionID   string                   `json:"region_id"`
		Themes     []string                 `json:"themes"`
		Candidates []domain.CandidateTarget `json:"candidates"`
	}{
		VersionID:  out.VersionID,
		PagePath:   out.PagePath,
		RegionID:   out.RegionID,
		Themes:     out.Themes,
		Candidates: fpCandidates,
	})
	sum := sha256.Sum256(fpBytes)
	out.ShortlistFingerprint = "sha256:" + hex.EncodeToString(sum[:])
	return out
}

func (w *Worker) appendSiblingFallbackIfEmpty(
	tx app.WriteTx,
	run domain.ScanRun,
	focalItem domain.JobItem,
	focalCV domain.ScanCapturedVersion,
	focalDoc domain.Document,
	shortlist domain.CandidateShortlist,
) domain.CandidateShortlist {
	if len(shortlist.Candidates) > 0 || focalItem.UnitKind != "region" || focalItem.PagePath == "" {
		return shortlist
	}
	regs, err := tx.VersionRegionsAfter(focalItem.VersionID, focalItem.PagePath, "", "", maxShortlistCandidates+1)
	if err != nil || len(regs) == 0 {
		return shortlist
	}
	var cands []domain.CandidateTarget
	for _, r := range regs {
		if r.ID == "" || r.ID == focalItem.RegionID {
			continue
		}
		sig, sErr := extractScopeThemes(tx, focalCV, focalItem.PagePath, r.ID)
		title := focalDoc.Title
		if sErr == nil && sig.Title != "" {
			title = sig.Title
		}
		cands = append(cands, domain.CandidateTarget{
			ProjectID:   focalCV.ProjectID,
			DocumentID:  focalDoc.ID,
			VersionID:   focalCV.VersionID,
			PagePath:    focalItem.PagePath,
			RegionID:    r.ID,
			UnitKind:    "region",
			TargetSetID: focalCV.TargetSetID,
			Title:       title,
			Score:       1.0,
			Signals: []domain.CandidateSignal{
				{
					Kind:   "same_page_sibling",
					Weight: 1.0,
					Detail: r.ID,
				},
			},
		})
		if len(cands) >= maxShortlistCandidates {
			break
		}
	}
	if len(cands) == 0 {
		return shortlist
	}
	out := shortlist
	out.Candidates = cands
	fpCandidates := make([]domain.CandidateTarget, len(cands))
	copy(fpCandidates, cands)
	for i := range fpCandidates {
		fpCandidates[i].TargetSetID = ""
	}
	fpBytes, _ := json.Marshal(struct {
		VersionID  string                   `json:"version_id"`
		PagePath   string                   `json:"page_path"`
		RegionID   string                   `json:"region_id"`
		Themes     []string                 `json:"themes"`
		Candidates []domain.CandidateTarget `json:"candidates"`
	}{
		VersionID:  out.VersionID,
		PagePath:   out.PagePath,
		RegionID:   out.RegionID,
		Themes:     out.Themes,
		Candidates: fpCandidates,
	})
	sum := sha256.Sum256(fpBytes)
	out.ShortlistFingerprint = "sha256:" + hex.EncodeToString(sum[:])
	return out
}

const unverifiedNominationRoutingHintPrefix = "unverified_card_routing_hint: "

func formatUnverifiedNominationHint(rationale string) string {
	trimmed := strings.TrimSpace(rationale)
	if strings.HasPrefix(trimmed, unverifiedNominationRoutingHintPrefix) {
		return trimmed
	}
	return unverifiedNominationRoutingHintPrefix + trimmed
}
