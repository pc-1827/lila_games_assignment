package main

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/heroiclabs/nakama-common/runtime"
)

const (
	profileCollection = "profile"
	profileKey        = "stats"
)

type userProfile struct {
	Wins   int64 `json:"wins"`
	Losses int64 `json:"losses"`
	Draws  int64 `json:"draws"`
}

func defaultProfile() *userProfile {
	return &userProfile{Wins: 0, Losses: 0, Draws: 0}
}

func rpcGetProfile(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {
	userID, err := getUserIDFromContext(ctx)
	if err != nil {
		return "", err
	}

	profile, err := readUserProfile(ctx, nk, userID)
	if err != nil {
		logger.Error("error reading profile for %s: %v", userID, err)
		return "", errInternalError
	}

	out, err := json.Marshal(profile)
	if err != nil {
		return "", errMarshal
	}

	return string(out), nil
}

func readUserProfile(ctx context.Context, nk runtime.NakamaModule, userID string) (*userProfile, error) {
	objects, err := nk.StorageRead(ctx, []*runtime.StorageRead{{
		Collection: profileCollection,
		Key:        profileKey,
		UserID:     userID,
	}})
	if err != nil {
		return nil, err
	}

	profile := defaultProfile()
	if len(objects) == 0 {
		return profile, nil
	}

	if err := json.Unmarshal([]byte(objects[0].GetValue()), profile); err != nil {
		return nil, err
	}

	return profile, nil
}

func updateUserProfileStat(ctx context.Context, nk runtime.NakamaModule, userID string, field string) error {
	reads, err := nk.StorageRead(ctx, []*runtime.StorageRead{{
		Collection: profileCollection,
		Key:        profileKey,
		UserID:     userID,
	}})
	if err != nil {
		return err
	}

	profile := defaultProfile()
	version := ""
	if len(reads) > 0 {
		version = reads[0].GetVersion()
		if err := json.Unmarshal([]byte(reads[0].GetValue()), profile); err != nil {
			return err
		}
	}

	switch field {
	case "wins":
		profile.Wins++
	case "losses":
		profile.Losses++
	case "draws":
		profile.Draws++
	}

	value, err := json.Marshal(profile)
	if err != nil {
		return err
	}

	_, err = nk.StorageWrite(ctx, []*runtime.StorageWrite{{
		Collection:      profileCollection,
		Key:             profileKey,
		UserID:          userID,
		Value:           string(value),
		PermissionRead:  2,
		PermissionWrite: 0,
		Version:         version,
	}})

	return err
}
