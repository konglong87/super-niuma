package sessioncontrol

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	operationMetadataSchema = "niuma.session-control-operation.v1"
	operationMetadataField  = "session_control_operation"
)

// OperationIdentity separates lookup-by-key from semantic conflict detection.
// Only hashes cross the service boundary; transports and stores never persist
// the caller's raw idempotency key.
type OperationIdentity struct {
	Schema      string    `json:"schema"`
	Operation   Operation `json:"operation"`
	OperationID string    `json:"operation_id"`
	KeyHash     string    `json:"key_hash"`
	Fingerprint string    `json:"fingerprint"`
}

type OperationMetadata struct {
	Schema      string    `json:"schema"`
	Operation   Operation `json:"operation"`
	OperationID string    `json:"operation_id"`
	KeyHash     string    `json:"key_hash"`
	Fingerprint string    `json:"fingerprint"`
}

func newOperationIdentity(operation Operation, requestContext RequestContext, idempotencyKey, fingerprint string) (OperationIdentity, error) {
	if operation == "" || validateRequestContext(requestContext) != nil || strings.TrimSpace(idempotencyKey) == "" || !validOperationHash(fingerprint) {
		return OperationIdentity{}, invalidState("operation identity is incomplete")
	}
	keyPayload := struct {
		Schema      string    `json:"schema"`
		Operation   Operation `json:"operation"`
		TenantID    uint64    `json:"tenant_id"`
		UserID      uint64    `json:"user_id"`
		ActorUserID uint64    `json:"actor_user_id"`
		Key         string    `json:"key"`
	}{operationMetadataSchema, operation, requestContext.TenantID, requestContext.UserID, requestContext.ActorUserID, idempotencyKey}
	keyHash := canonicalSHA256(keyPayload)
	operationPayload := struct {
		Schema      string    `json:"schema"`
		Operation   Operation `json:"operation"`
		KeyHash     string    `json:"key_hash"`
		Fingerprint string    `json:"fingerprint"`
	}{operationMetadataSchema, operation, keyHash, fingerprint}
	return OperationIdentity{
		Schema:      operationMetadataSchema,
		Operation:   operation,
		OperationID: canonicalSHA256(operationPayload),
		KeyHash:     keyHash,
		Fingerprint: fingerprint,
	}, nil
}

func (identity OperationIdentity) Metadata() OperationMetadata {
	return OperationMetadata(identity)
}

func encodeOperationMetadata(identity OperationIdentity) (string, error) {
	if err := validateOperationIdentity(identity); err != nil {
		return "", err
	}
	payload, err := json.Marshal(identity.Metadata())
	if err != nil {
		return "", fmt.Errorf("encode operation metadata: %w", err)
	}
	return string(payload), nil
}

func EncodeOperationMetadataJSON(identity OperationIdentity) (string, error) {
	return encodeOperationMetadata(identity)
}

func decodeOperationMetadata(payload string) (OperationMetadata, error) {
	decoder := json.NewDecoder(bytes.NewBufferString(payload))
	decoder.DisallowUnknownFields()
	var metadata OperationMetadata
	if err := decoder.Decode(&metadata); err != nil {
		return OperationMetadata{}, invalidState("operation metadata is invalid")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return OperationMetadata{}, invalidState("operation metadata contains trailing data")
	}
	if err := validateOperationMetadata(metadata); err != nil {
		return OperationMetadata{}, err
	}
	return metadata, nil
}

func DecodeOperationMetadataJSON(payload string) (OperationMetadata, error) {
	return decodeOperationMetadata(payload)
}

func mergeOperationMetadataJSON(payload string, identity OperationIdentity) (string, error) {
	if err := validateOperationIdentity(identity); err != nil {
		return "", err
	}
	document := make(map[string]json.RawMessage)
	if strings.TrimSpace(payload) != "" {
		if err := json.Unmarshal([]byte(payload), &document); err != nil || document == nil {
			return "", invalidState("resource metadata must be a JSON object")
		}
	}
	if _, exists := document[operationMetadataField]; exists {
		return "", invalidState("resource metadata already contains an operation identity")
	}
	metadata, err := json.Marshal(identity.Metadata())
	if err != nil {
		return "", fmt.Errorf("encode embedded operation metadata: %w", err)
	}
	document[operationMetadataField] = metadata
	encoded, err := json.Marshal(document)
	if err != nil {
		return "", fmt.Errorf("encode resource metadata: %w", err)
	}
	return string(encoded), nil
}

func operationMetadataFromJSON(payload string) (OperationMetadata, error) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &document); err != nil || document == nil {
		return OperationMetadata{}, invalidState("resource metadata must be a JSON object")
	}
	raw := document[operationMetadataField]
	if len(raw) == 0 {
		return OperationMetadata{}, invalidState("resource metadata has no operation identity")
	}
	return decodeOperationMetadata(string(raw))
}

func validateRecoveredMetadata(expected OperationIdentity, stored OperationMetadata) error {
	if err := validateOperationIdentity(expected); err != nil {
		return err
	}
	if err := validateOperationMetadata(stored); err != nil {
		return err
	}
	if stored.Operation != expected.Operation || stored.KeyHash != expected.KeyHash {
		return invalidState("recovered operation identity does not match the requested key")
	}
	if stored.Fingerprint != expected.Fingerprint {
		return &ServiceError{Code: CodeIdempotencyConflict, Message: "idempotency key was used for a different request"}
	}
	if stored.OperationID != expected.OperationID {
		return invalidState("recovered operation ID does not match its fingerprint")
	}
	return nil
}

func validateOperationIdentity(identity OperationIdentity) error {
	return validateOperationMetadata(identity.Metadata())
}

func validateOperationMetadata(metadata OperationMetadata) error {
	if metadata.Schema != operationMetadataSchema || metadata.Operation == "" || !validOperationHash(metadata.OperationID) || !validOperationHash(metadata.KeyHash) || !validOperationHash(metadata.Fingerprint) {
		return invalidState("operation metadata is invalid")
	}
	expectedID := canonicalSHA256(struct {
		Schema      string    `json:"schema"`
		Operation   Operation `json:"operation"`
		KeyHash     string    `json:"key_hash"`
		Fingerprint string    `json:"fingerprint"`
	}{metadata.Schema, metadata.Operation, metadata.KeyHash, metadata.Fingerprint})
	if metadata.OperationID != expectedID {
		return invalidState("operation metadata hash is invalid")
	}
	return nil
}

func canonicalSHA256(value any) string {
	payload, _ := json.Marshal(value)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func validOperationHash(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
