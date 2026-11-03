// Package handoff defines the bounded, immutable SessionHandoffPackage v1
// contract. It deliberately contains structured facts and evidence locators,
// never source transcripts or raw source records.
package handoff

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/konglong87/super-niuma/internal/compact"
)

const (
	SchemaV1                 = "niuma.session-handoff.v1"
	PackageIDPrefix          = "handoff:"
	DefaultPackageTokenLimit = 2048
	MaxCandidateBytes        = 512
	MaxEvidenceEntryIDBytes  = 128

	SourceKindTenant = "tenant"
	SourceKindLocal  = "local"

	CursorPrefixTaskEvent      = "task_event"
	CursorPrefixMessage        = "message"
	CursorPrefixToolTrace      = "tool_trace"
	CursorPrefixFileChange     = "file_change"
	CursorPrefixCapabilityLoop = "capability_loop"
	CursorPrefixEntry          = "entry"
	CursorPrefixLine           = "line"
)

type ErrorCode string

const (
	CodeUnsupportedSchema ErrorCode = "unsupported_schema"
	CodeInvalidRef        ErrorCode = "invalid_ref"
	CodeInvalidCursor     ErrorCode = "invalid_cursor"
	CodeInvalidHash       ErrorCode = "invalid_hash"
	CodeDuplicateLocator  ErrorCode = "duplicate_locator"
	CodeInvalidEvidence   ErrorCode = "invalid_evidence"
	CodeInvalidPackage    ErrorCode = "invalid_package"
	CodeSourceChanged     ErrorCode = "source_changed"
	CodeUnsupportedSource ErrorCode = "unsupported_source"
)

// Error is transport-neutral so later service and event layers can expose the
// same stable contract without retaining source content.
type Error struct {
	Code    ErrorCode
	Message string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Message == "" {
		return string(e.Code)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func ErrorCodeOf(err error) ErrorCode {
	if typed, ok := err.(*Error); ok && typed != nil {
		return typed.Code
	}
	return ""
}

type Verification string

const (
	VerificationReported Verification = "reported"
	VerificationVerified Verification = "verified"
)

type VerifierKind string

const (
	VerifierToolTrace      VerifierKind = "tool_trace"
	VerifierFileChange     VerifierKind = "file_change"
	VerifierTest           VerifierKind = "test"
	VerifierReadback       VerifierKind = "readback"
	VerifierCapabilityLoop VerifierKind = "capability_loop"
)

// Source identifies a fixed, previously captured source snapshot. CapturedAt
// is operational metadata and intentionally omitted from every identity hash.
type Source struct {
	Ref           string    `json:"ref"`
	Cursor        string    `json:"cursor"`
	ContentSHA256 string    `json:"content_sha256"`
	CapturedAt    time.Time `json:"captured_at"`
}

type Target struct {
	Ref string `json:"ref"`
}

type Verifier struct {
	Kind   VerifierKind `json:"kind"`
	Ref    string       `json:"ref"`
	SHA256 string       `json:"sha256"`
}

// Evidence is an opaque locator and a hash of its canonical source record.
// Claim prose is display-only; it cannot establish verified status by itself.
type Evidence struct {
	Ref          string       `json:"ref"`
	Claim        string       `json:"claim"`
	Verification Verification `json:"verification"`
	SHA256       string       `json:"sha256"`
	Verifier     Verifier     `json:"verifier,omitempty"`
}

type Budget struct {
	EstimatedTokens int `json:"estimated_tokens"`
	LimitTokens     int `json:"limit_tokens"`
}

// Package is the v1 transport contract. PackageSHA256 hashes the canonical
// package body, which excludes this field and Source.CapturedAt to avoid
// metadata-dependent identities and self-referential hashing.
type Package struct {
	Schema        string     `json:"schema"`
	PackageID     string     `json:"package_id"`
	PackageSHA256 string     `json:"package_sha256"`
	Source        Source     `json:"source"`
	Target        Target     `json:"target"`
	Objective     string     `json:"objective"`
	Constraints   []string   `json:"constraints"`
	StageSummary  string     `json:"stage_summary"`
	Completed     []string   `json:"completed"`
	OpenItems     []string   `json:"open_items"`
	Risks         []string   `json:"risks"`
	NextActions   []string   `json:"next_actions"`
	Evidence      []Evidence `json:"evidence"`
	Budget        Budget     `json:"budget"`
}

type PackageInput struct {
	Source       Source
	Target       Target
	Objective    string
	Constraints  []string
	StageSummary string
	Completed    []string
	OpenItems    []string
	Risks        []string
	NextActions  []string
	Evidence     []Evidence
	Budget       Budget
}

// BuildPackage derives every identity from normalized explicit fields. It is
// intentionally pure: adapters capture bounded records before calling it.
func BuildPackage(input PackageInput) (Package, error) {
	pkg := Package{
		Schema:       SchemaV1,
		Source:       input.Source,
		Target:       input.Target,
		Objective:    normalizeText(input.Objective),
		Constraints:  normalizeStrings(input.Constraints),
		StageSummary: normalizeText(input.StageSummary),
		Completed:    normalizeStrings(input.Completed),
		OpenItems:    normalizeStrings(input.OpenItems),
		Risks:        normalizeStrings(input.Risks),
		NextActions:  normalizeStrings(input.NextActions),
		Evidence:     normalizeEvidence(input.Evidence),
		Budget:       input.Budget,
	}
	pkg.Source.ContentSHA256 = ""
	if err := validateShape(pkg, false); err != nil {
		return Package{}, err
	}

	sourcePayload, err := pkg.canonicalSourcePayload()
	if err != nil {
		return Package{}, err
	}
	pkg.Source.ContentSHA256 = hashPayload(sourcePayload)
	pkg.PackageID = packageID(pkg.Source)
	if pkg.Budget.EstimatedTokens == 0 {
		for range 3 {
			payload, estimateErr := pkg.CanonicalPayload()
			if estimateErr != nil {
				return Package{}, estimateErr
			}
			estimate := compact.EstimateTextTokens(string(payload))
			if estimate == pkg.Budget.EstimatedTokens {
				break
			}
			pkg.Budget.EstimatedTokens = estimate
		}
	}
	packagePayload, err := pkg.CanonicalPayload()
	if err != nil {
		return Package{}, err
	}
	pkg.PackageSHA256 = hashPayload(packagePayload)
	return pkg, pkg.Validate()
}

// Validate checks schema, locators, cursor namespace, evidence verification,
// and both independently recomputed hashes. A reported item therefore cannot
// become verified without a structured verifier record.
func (p Package) Validate() error {
	if err := validateShape(p, true); err != nil {
		return err
	}
	if p.PackageID != packageID(p.Source) {
		return contractError(CodeInvalidPackage, "package_id does not match source identity")
	}
	payload, err := p.CanonicalPayload()
	if err != nil {
		return err
	}
	if p.PackageSHA256 != hashPayload(payload) {
		return contractError(CodeInvalidHash, "package_sha256 does not match canonical package body")
	}
	return nil
}

// CanonicalPayload serializes the package body in a stable order. It omits
// captured_at and package_sha256 because neither is source content and the
// latter would make the payload self-referential.
func (p Package) CanonicalPayload() ([]byte, error) {
	if err := validateShape(p, false); err != nil {
		return nil, err
	}
	return json.Marshal(canonicalPackageBody{
		Schema:      p.Schema,
		PackageID:   p.PackageID,
		Source:      canonicalSource{Ref: p.Source.Ref, Cursor: p.Source.Cursor, ContentSHA256: p.Source.ContentSHA256},
		Target:      p.Target,
		Objective:   normalizeText(p.Objective),
		Constraints: normalizeStrings(p.Constraints),
		Stage:       normalizeText(p.StageSummary),
		Completed:   normalizeStrings(p.Completed),
		OpenItems:   normalizeStrings(p.OpenItems),
		Risks:       normalizeStrings(p.Risks),
		NextActions: normalizeStrings(p.NextActions),
		Evidence:    normalizeEvidence(p.Evidence),
		Budget:      p.Budget,
	})
}

func (p Package) canonicalSourcePayload() ([]byte, error) {
	if err := validateShape(p, false); err != nil {
		return nil, err
	}
	return json.Marshal(canonicalSourceBody{
		Schema:      p.Schema,
		Source:      canonicalSource{Ref: p.Source.Ref, Cursor: p.Source.Cursor},
		Objective:   normalizeText(p.Objective),
		Constraints: normalizeStrings(p.Constraints),
		Stage:       normalizeText(p.StageSummary),
		Completed:   normalizeStrings(p.Completed),
		OpenItems:   normalizeStrings(p.OpenItems),
		Risks:       normalizeStrings(p.Risks),
		NextActions: normalizeStrings(p.NextActions),
		Evidence:    canonicalEvidenceIdentities(p.Evidence),
	})
}

type canonicalSource struct {
	Ref           string `json:"ref"`
	Cursor        string `json:"cursor"`
	ContentSHA256 string `json:"content_sha256,omitempty"`
}

type canonicalSourceBody struct {
	Schema      string                      `json:"schema"`
	Source      canonicalSource             `json:"source"`
	Objective   string                      `json:"objective"`
	Constraints []string                    `json:"constraints"`
	Stage       string                      `json:"stage_summary"`
	Completed   []string                    `json:"completed"`
	OpenItems   []string                    `json:"open_items"`
	Risks       []string                    `json:"risks"`
	NextActions []string                    `json:"next_actions"`
	Evidence    []canonicalEvidenceIdentity `json:"evidence"`
}

// canonicalEvidenceIdentity intentionally excludes Claim. Claim is mutable
// display prose: package integrity covers it, while source identity does not.
type canonicalEvidenceIdentity struct {
	Ref          string       `json:"ref"`
	Verification Verification `json:"verification"`
	SHA256       string       `json:"sha256"`
	Verifier     Verifier     `json:"verifier"`
}

type canonicalPackageBody struct {
	Schema      string          `json:"schema"`
	PackageID   string          `json:"package_id"`
	Source      canonicalSource `json:"source"`
	Target      Target          `json:"target"`
	Objective   string          `json:"objective"`
	Constraints []string        `json:"constraints"`
	Stage       string          `json:"stage_summary"`
	Completed   []string        `json:"completed"`
	OpenItems   []string        `json:"open_items"`
	Risks       []string        `json:"risks"`
	NextActions []string        `json:"next_actions"`
	Evidence    []Evidence      `json:"evidence"`
	Budget      Budget          `json:"budget"`
}

func validateShape(p Package, requireHashes bool) error {
	if p.Schema != SchemaV1 {
		return contractError(CodeUnsupportedSchema, "unsupported handoff schema")
	}
	sourceKind, err := validateRef(p.Source.Ref)
	if err != nil {
		return err
	}
	targetKind, err := validateRef(p.Target.Ref)
	if err != nil {
		return err
	}
	if targetKind != SourceKindTenant {
		return contractError(CodeInvalidRef, "handoff target must be a tenant session")
	}
	if err := validateCursor(sourceKind, p.Source.Cursor); err != nil {
		return err
	}
	if requireHashes && !isSHA256(p.Source.ContentSHA256) {
		return contractError(CodeInvalidHash, "content_sha256 must be lowercase SHA-256")
	}
	if requireHashes && !isSHA256(p.PackageSHA256) {
		return contractError(CodeInvalidHash, "package_sha256 must be lowercase SHA-256")
	}
	if p.Budget.EstimatedTokens < 0 || p.Budget.LimitTokens <= 0 || p.Budget.LimitTokens > DefaultPackageTokenLimit {
		return contractError(CodeInvalidPackage, "budget limit must be positive and at most the approved maximum")
	}
	locators := make(map[string]struct{}, len(p.Evidence))
	for _, evidence := range p.Evidence {
		if err := validateEvidence(p.Source.Ref, evidence); err != nil {
			return err
		}
		if _, exists := locators[evidence.Ref]; exists {
			return contractError(CodeDuplicateLocator, "evidence locator must be unique")
		}
		locators[evidence.Ref] = struct{}{}
	}
	return nil
}

func validateRef(ref string) (string, error) {
	kind, key, ok := strings.Cut(ref, ":")
	if !ok || (kind != SourceKindTenant && kind != SourceKindLocal) || !validRefKey(key) {
		return "", contractError(CodeInvalidRef, "session ref must be tenant:<key> or local:<key>")
	}
	return kind, nil
}

func validRefKey(key string) bool {
	if key == "" {
		return false
	}
	for _, r := range key {
		if unicode.IsSpace(r) || unicode.IsControl(r) || r == '/' || r == '\\' || r == ':' || r == '#' {
			return false
		}
	}
	return true
}

func validateCursor(sourceKind, cursor string) error {
	prefix, value, ok := strings.Cut(cursor, ":")
	if !ok {
		return contractError(CodeInvalidCursor, "cursor must be a typed logical cursor")
	}
	switch sourceKind {
	case SourceKindTenant:
		if (prefix == CursorPrefixTaskEvent || prefix == CursorPrefixMessage) && validCanonicalDecimal(value, false) {
			return nil
		}
	case SourceKindLocal:
		if prefix == CursorPrefixEntry && validSafeEntryID(value) {
			return nil
		}
		if prefix == CursorPrefixLine && validCanonicalDecimal(value, false) {
			return nil
		}
	}
	return contractError(CodeInvalidCursor, "cursor value or prefix is not valid for source kind")
}

func allDecimalDigits(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validateEvidence(sourceRef string, e Evidence) error {
	if !validEvidenceRef(sourceRef, e.Ref) {
		return contractError(CodeInvalidEvidence, "evidence ref must be an opaque logical locator")
	}
	if !isSHA256(e.SHA256) {
		return contractError(CodeInvalidHash, "evidence sha256 must be lowercase SHA-256")
	}
	switch e.Verification {
	case VerificationReported:
		if !emptyVerifier(e.Verifier) && !validVerifier(sourceRef, e.Verifier) {
			return contractError(CodeInvalidEvidence, "reported evidence verifier must be a structured source locator")
		}
		return nil
	case VerificationVerified:
		if !validVerifier(sourceRef, e.Verifier) {
			return contractError(CodeInvalidEvidence, "verified evidence requires a structured verifier")
		}
		return nil
	default:
		return contractError(CodeInvalidEvidence, "unknown evidence verification")
	}
}

func validEvidenceRef(sourceRef, ref string) bool {
	refSource, locator, ok := strings.Cut(ref, "#")
	if !ok || refSource != sourceRef || locator == "" {
		return false
	}
	sourceKind, err := validateRef(refSource)
	if err != nil {
		return false
	}
	switch sourceKind {
	case SourceKindTenant:
		return validTenantEvidenceLocator(locator)
	case SourceKindLocal:
		return validLocalEvidenceLocator(locator)
	default:
		return false
	}
}

func validTenantEvidenceLocator(locator string) bool {
	kind, value, ok := strings.Cut(locator, ":")
	if !ok {
		return false
	}
	switch kind {
	case CursorPrefixTaskEvent, CursorPrefixMessage:
		return validCanonicalDecimal(value, false)
	case CursorPrefixToolTrace:
		eventID, ordinal, ok := strings.Cut(value, ":")
		return ok && validCanonicalDecimal(eventID, false) && validCanonicalDecimal(ordinal, true)
	default:
		return false
	}
}

func validLocalEvidenceLocator(locator string) bool {
	kind, value, ok := strings.Cut(locator, ":")
	if !ok {
		return false
	}
	switch kind {
	case CursorPrefixEntry:
		return validSafeEntryID(value)
	case CursorPrefixLine:
		return validCanonicalDecimal(value, false)
	default:
		return false
	}
}

func validSafeEntryID(value string) bool {
	if value == "" || len(value) > MaxEvidenceEntryIDBytes {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return value != "." && value != ".."
}

func validCanonicalDecimal(value string, allowZero bool) bool {
	if value == "" || !allDecimalDigits(value) || len(value) > 1 && value[0] == '0' {
		return false
	}
	number, err := strconv.ParseUint(value, 10, 64)
	return err == nil && (allowZero || number > 0)
}

func emptyVerifier(verifier Verifier) bool {
	return verifier.Kind == "" && verifier.Ref == "" && verifier.SHA256 == ""
}

func validVerifier(sourceRef string, verifier Verifier) bool {
	switch verifier.Kind {
	case VerifierToolTrace, VerifierFileChange, VerifierTest, VerifierReadback, VerifierCapabilityLoop:
	default:
		return false
	}
	return validEvidenceRef(sourceRef, verifier.Ref) && isSHA256(verifier.SHA256)
}

func canonicalEvidenceIdentities(evidence []Evidence) []canonicalEvidenceIdentity {
	normalized := normalizeEvidence(evidence)
	identities := make([]canonicalEvidenceIdentity, 0, len(normalized))
	for _, item := range normalized {
		identities = append(identities, canonicalEvidenceIdentity{
			Ref:          item.Ref,
			Verification: item.Verification,
			SHA256:       item.SHA256,
			Verifier:     item.Verifier,
		})
	}
	return identities
}

func isSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

func hashPayload(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func packageID(source Source) string {
	return PackageIDPrefix + source.Ref + ":" + source.Cursor + ":" + source.ContentSHA256
}

func contractError(code ErrorCode, message string) error {
	return &Error{Code: code, Message: message}
}
