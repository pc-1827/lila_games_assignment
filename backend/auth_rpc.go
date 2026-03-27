package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/heroiclabs/nakama-common/runtime"
)

type checkUsernameRequest struct {
	Username string `json:"username"`
}

type checkUsernameResponse struct {
	Exists bool `json:"exists"`
}

func rpcCheckUsername(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {
	_ = nk

	req := &checkUsernameRequest{}
	if err := json.Unmarshal([]byte(payload), req); err != nil {
		return "", errUnmarshal
	}

	username := strings.TrimSpace(strings.ToLower(req.Username))
	if username == "" {
		return "", runtime.NewError("username is required", 3)
	}

	var exists bool
	query := `SELECT EXISTS(SELECT 1 FROM users WHERE username = $1)`
	if err := db.QueryRowContext(ctx, query, username).Scan(&exists); err != nil {
		logger.Error("error checking username existence: %v", err)
		return "", errInternalError
	}

	out, err := json.Marshal(&checkUsernameResponse{Exists: exists})
	if err != nil {
		return "", errMarshal
	}

	return string(out), nil
}
