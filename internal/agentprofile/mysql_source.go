package agentprofile

import (
	"context"
	"errors"
	"fmt"
	"time"

	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

type ProfileStore interface {
	ListAgentProfiles(ctx context.Context, tenantID, ownerUserID uint64, status string, limit int) ([]mysqlstore.AgentProfile, error)
	GetAgentProfile(ctx context.Context, tenantID, profileID uint64) (mysqlstore.AgentProfile, error)
	GetAgentProfileAssignment(ctx context.Context, tenantID, userID uint64, surface string) (mysqlstore.AgentProfileAssignment, error)
}

type MySQLSource struct {
	store ProfileStore
}

func NewMySQLSource(store ProfileStore) MySQLSource {
	return MySQLSource{store: store}
}

func (s MySQLSource) FindPublishedProfile(ctx context.Context, tenantID, userID uint64, key string, version uint) (Profile, error) {
	if s.store == nil || tenantID == 0 || key == "" {
		return Profile{}, ErrProfileNotFound
	}
	profiles, err := s.store.ListAgentProfiles(ctx, tenantID, userID, string(StatusPublished), 500)
	if err != nil {
		return Profile{}, err
	}
	for _, record := range profiles {
		if record.ProfileKey != key || (version != 0 && record.ProfileVersion != version) || record.Status != string(StatusPublished) {
			continue
		}
		return profileFromStorage(record)
	}
	return Profile{}, ErrProfileNotFound
}

func (s MySQLSource) FindAssignment(ctx context.Context, tenantID, userID uint64, surface string) (ProfileAssignment, error) {
	if s.store == nil || tenantID == 0 || userID == 0 || surface == "" {
		return ProfileAssignment{}, ErrAssignmentNotFound
	}
	assignment, err := s.store.GetAgentProfileAssignment(ctx, tenantID, userID, surface)
	if errors.Is(err, mysqlstore.ErrNotFound) {
		return ProfileAssignment{}, ErrAssignmentNotFound
	}
	if err != nil {
		return ProfileAssignment{}, err
	}
	profile, err := s.store.GetAgentProfile(ctx, tenantID, assignment.ProfileID)
	if errors.Is(err, mysqlstore.ErrNotFound) {
		return ProfileAssignment{}, ErrAssignmentNotFound
	}
	if err != nil {
		return ProfileAssignment{}, err
	}
	return ProfileAssignment{ProfileKey: profile.ProfileKey, ProfileVersion: profile.ProfileVersion}, nil
}

func profileFromStorage(record mysqlstore.AgentProfile) (Profile, error) {
	document, err := DecodeProfileDocument([]byte(record.ConfigJSON))
	if err != nil {
		return Profile{}, fmt.Errorf("decode stored profile %q: %w", record.ProfileKey, err)
	}
	return Profile{
		ID: record.ID, TenantID: record.TenantID, OwnerUserID: record.OwnerUserID, Key: record.ProfileKey,
		Scope: Scope(record.Scope), DisplayName: record.DisplayName, Description: record.Description,
		Version: record.ProfileVersion, Status: Status(record.Status), Config: document,
		RequestedHash: record.RequestedHash, EffectiveHash: record.EffectiveHash, PublishedAt: timePtr(record.PublishedAt),
	}, nil
}

func timePtr(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}
