package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/heroiclabs/nakama-common/runtime"
	"github.com/heroiclabs/nakama-project-template/api"
	"google.golang.org/protobuf/encoding/protojson"
)

type nakamaRpcFunc func(context.Context, runtime.Logger, *sql.DB, runtime.NakamaModule, string) (string, error)

type createRoomRequest struct {
	Fast bool `json:"fast"`
}

type createRoomResponse struct {
	MatchID string `json:"match_id"`
}

type listRoomsRequest struct {
	Limit int  `json:"limit"`
	Fast  *int `json:"fast,omitempty"` // 0 or 1
}

type roomInfo struct {
	MatchID  string `json:"match_id"`
	Size     int32  `json:"size"`
	MaxSize  int32  `json:"max_size"`
	Label    string `json:"label"`
	IsOpen   bool   `json:"is_open"`
	FastMode bool   `json:"fast_mode"`
}

type listRoomsResponse struct {
	Rooms []roomInfo `json:"rooms"`
}

type joinRoomRequest struct {
	MatchID string `json:"match_id"`
}

type joinRoomResponse struct {
	MatchID  string `json:"match_id"`
	Joinable bool   `json:"joinable"`
}

type surrenderRequest struct {
	MatchID string `json:"match_id"`
}

type closeRoomRequest struct {
	MatchID string `json:"match_id"`
}

type statusResponse struct {
	Ok bool `json:"ok"`
}

func getUserIDFromContext(ctx context.Context) (string, error) {
	userID, ok := ctx.Value(runtime.RUNTIME_CTX_USER_ID).(string)
	if !ok || userID == "" {
		return "", errNoUserIdFound
	}
	return userID, nil
}

func rpcFindMatch(marshaler *protojson.MarshalOptions, unmarshaler *protojson.UnmarshalOptions) nakamaRpcFunc {
	return func(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {
		if _, err := getUserIDFromContext(ctx); err != nil {
			return "", err
		}

		request := &api.RpcFindMatchRequest{}
		if payload != "" && payload != "{}" {
			if err := unmarshaler.Unmarshal([]byte(payload), request); err != nil {
				return "", errUnmarshal
			}
		}

		maxSize := 1
		fast := 0
		if request.Fast {
			fast = 1
		}
		query := fmt.Sprintf("+label.open:1 +label.fast:%d", fast)

		matchIDs := make([]string, 0, 10)
		matches, err := nk.MatchList(ctx, 10, true, "", nil, &maxSize, query)
		if err != nil {
			logger.Error("error listing matches: %v", err)
			return "", errInternalError
		}

		if len(matches) > 0 {
			for _, match := range matches {
				matchIDs = append(matchIDs, match.MatchId)
			}
		}

		response, err := marshaler.Marshal(&api.RpcFindMatchResponse{MatchIds: matchIDs})
		if err != nil {
			logger.Error("error marshaling response payload: %v", err)
			return "", errMarshal
		}
		return string(response), nil
	}
}

func rpcCreateRoom(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {
	if _, err := getUserIDFromContext(ctx); err != nil {
		return "", err
	}

	request := &createRoomRequest{}
	if payload != "" && payload != "{}" {
		if err := json.Unmarshal([]byte(payload), request); err != nil {
			return "", errUnmarshal
		}
	}

	matchID, err := nk.MatchCreate(ctx, moduleName, map[string]interface{}{"fast": request.Fast})
	if err != nil {
		logger.Error("error creating room: %v", err)
		return "", errInternalError
	}

	response, err := json.Marshal(&createRoomResponse{MatchID: matchID})
	if err != nil {
		return "", errMarshal
	}
	return string(response), nil
}

func rpcListRooms(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {
	if _, err := getUserIDFromContext(ctx); err != nil {
		return "", err
	}

	request := &listRoomsRequest{Limit: 20}
	if payload != "" && payload != "{}" {
		if err := json.Unmarshal([]byte(payload), request); err != nil {
			return "", errUnmarshal
		}
	}
	if request.Limit <= 0 || request.Limit > 50 {
		request.Limit = 20
	}

	maxSize := 1
	query := "+label.open:1"
	if request.Fast != nil {
		query = fmt.Sprintf("+label.open:1 +label.fast:%d", *request.Fast)
	}

	matches, err := nk.MatchList(ctx, request.Limit, true, "", nil, &maxSize, query)
	if err != nil {
		logger.Error("error listing rooms: %v", err)
		return "", errInternalError
	}

	rooms := make([]roomInfo, 0, len(matches))
	for _, match := range matches {
		if match.GetSize() <= 0 {
			continue
		}

		label := ""
		if match.GetLabel() != nil {
			label = match.GetLabel().GetValue()
		}
		isOpen := strings.Contains(label, `"open":1`)
		if !isOpen {
			continue
		}

		rooms = append(rooms, roomInfo{
			MatchID:  match.MatchId,
			Size:     match.Size,
			MaxSize:  2,
			Label:    label,
			IsOpen:   isOpen,
			FastMode: strings.Contains(label, `"fast":1`),
		})
	}

	response, err := json.Marshal(&listRoomsResponse{Rooms: rooms})
	if err != nil {
		return "", errMarshal
	}
	return string(response), nil
}

func rpcJoinRoom(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {
	if _, err := getUserIDFromContext(ctx); err != nil {
		return "", err
	}

	request := &joinRoomRequest{}
	if err := json.Unmarshal([]byte(payload), request); err != nil {
		return "", errUnmarshal
	}
	if request.MatchID == "" {
		return "", runtime.NewError("match_id is required", 3)
	}

	match, err := nk.MatchGet(ctx, request.MatchID)
	if err != nil {
		logger.Error("error fetching match %s: %v", request.MatchID, err)
		return "", errInternalError
	}
	if match == nil {
		return "", runtime.NewError("room not found", 5)
	}
	if !match.Authoritative {
		return "", runtime.NewError("room is not joinable", 9)
	}

	label := ""
	if match.GetLabel() != nil {
		label = match.GetLabel().GetValue()
	}
	if match.GetSize() >= 2 || !strings.Contains(label, `"open":1`) {
		return "", runtime.NewError("room is full or closed", 9)
	}

	response, err := json.Marshal(&joinRoomResponse{MatchID: request.MatchID, Joinable: true})
	if err != nil {
		return "", errMarshal
	}
	return string(response), nil
}

func rpcSurrender(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {
	userID, err := getUserIDFromContext(ctx)
	if err != nil {
		return "", err
	}

	request := &surrenderRequest{}
	if err := json.Unmarshal([]byte(payload), request); err != nil {
		return "", errUnmarshal
	}
	if request.MatchID == "" {
		return "", runtime.NewError("match_id is required", 3)
	}

	if match, err := nk.MatchGet(ctx, request.MatchID); err != nil {
		logger.Error("error fetching match %s: %v", request.MatchID, err)
		return "", errInternalError
	} else if match != nil && match.GetSize() <= 1 {
		if _, err := nk.MatchSignal(ctx, request.MatchID, "close_room:"+userID); err != nil {
			logger.Error("error signaling close_room to match %s: %v", request.MatchID, err)
			return "", errInternalError
		}

		response, err := json.Marshal(&statusResponse{Ok: true})
		if err != nil {
			return "", errMarshal
		}
		return string(response), nil
	}

	if _, err := nk.MatchSignal(ctx, request.MatchID, "surrender:"+userID); err != nil {
		logger.Error("error signaling surrender to match %s: %v", request.MatchID, err)
		return "", errInternalError
	}

	response, err := json.Marshal(&statusResponse{Ok: true})
	if err != nil {
		return "", errMarshal
	}
	return string(response), nil
}

func rpcCloseRoom(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {
	userID, err := getUserIDFromContext(ctx)
	if err != nil {
		return "", err
	}

	request := &closeRoomRequest{}
	if err := json.Unmarshal([]byte(payload), request); err != nil {
		return "", errUnmarshal
	}
	if request.MatchID == "" {
		return "", runtime.NewError("match_id is required", 3)
	}

	if _, err := nk.MatchSignal(ctx, request.MatchID, "close_room:"+userID); err != nil {
		logger.Error("error signaling close_room to match %s: %v", request.MatchID, err)
		return "", errInternalError
	}

	response, err := json.Marshal(&statusResponse{Ok: true})
	if err != nil {
		return "", errMarshal
	}
	return string(response), nil
}
