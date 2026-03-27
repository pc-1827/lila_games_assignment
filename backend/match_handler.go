// Copyright 2020 The Nakama Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/heroiclabs/nakama-common/runtime"
	"github.com/heroiclabs/nakama-project-template/api"
)

const (
	moduleName = "tic-tac-toe"

	tickRate = 5

	maxEmptySec = 30
	turnTimeSec = 30

	matchEndGraceSec = 3
)

var winningPositions = [][]int32{
	{0, 1, 2},
	{3, 4, 5},
	{6, 7, 8},
	{0, 3, 6},
	{1, 4, 7},
	{2, 5, 8},
	{0, 4, 8},
	{2, 4, 6},
}

// Compile-time check to make sure all required functions are implemented.
var _ runtime.Match = &MatchHandler{}

type MatchLabel struct {
	Open int `json:"open"`
	Fast int `json:"fast"`
}

type MatchHandler struct {
	marshaler   *protojson.MarshalOptions
	unmarshaler *protojson.UnmarshalOptions
}

type MatchState struct {
	label      *MatchLabel
	emptyTicks int

	// Currently connected users, or reserved spaces when value is nil.
	presences map[string]runtime.Presence
	// Number of users currently in the process of connecting to the match.
	joinsInProgress int

	playing bool

	// Finished means game ended; match will terminate shortly.
	finished             bool
	finishRemainingTicks int64

	board []api.Mark
	marks map[string]api.Mark
	mark  api.Mark

	deadlineRemainingTicks int64
	winner                 api.Mark
	winnerPositions        []int32

	// Track disconnect duration for in-game players.
	disconnectTicks map[string]int64

	// Set from MatchSignal, consumed in loop.
	pendingSurrenderUserID string
	pendingCloseRoomUserID string
}

func (ms *MatchState) ConnectedCount() int {
	count := 0
	for _, p := range ms.presences {
		if p != nil {
			count++
		}
	}
	return count
}

func (m *MatchHandler) MatchInit(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, params map[string]interface{}) (interface{}, int, string) {
	fast, _ := params["fast"].(bool)

	label := &MatchLabel{Open: 1}
	if fast {
		label.Fast = 1
	}
	labelJSON, err := json.Marshal(label)
	if err != nil {
		logger.WithField("error", err).Error("match init failed")
		labelJSON = []byte("{}")
	}

	state := &MatchState{
		label:           label,
		presences:       make(map[string]runtime.Presence, 2),
		disconnectTicks: make(map[string]int64, 2),
	}
	return state, tickRate, string(labelJSON)
}

func (m *MatchHandler) MatchJoinAttempt(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, dispatcher runtime.MatchDispatcher, tick int64, state interface{}, presence runtime.Presence, metadata map[string]string) (interface{}, bool, string) {
	s := state.(*MatchState)

	if s.finished {
		return s, false, "match complete"
	}

	if existingPresence, ok := s.presences[presence.GetUserId()]; ok {
		if existingPresence == nil {
			s.joinsInProgress++
			return s, true, ""
		}
		return s, false, "already joined"
	}

	if len(s.presences)+s.joinsInProgress >= 2 {
		return s, false, "match full"
	}

	s.joinsInProgress++
	return s, true, ""
}

func (m *MatchHandler) MatchJoin(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, dispatcher runtime.MatchDispatcher, tick int64, state interface{}, presences []runtime.Presence) interface{} {
	s := state.(*MatchState)
	t := time.Now().UTC()

	for _, presence := range presences {
		userID := presence.GetUserId()
		s.emptyTicks = 0
		s.presences[userID] = presence
		s.disconnectTicks[userID] = 0
		s.joinsInProgress--

		var opCode api.OpCode
		var msg proto.Message

		if s.playing {
			opCode = api.OpCode_OPCODE_UPDATE
			msg = &api.Update{
				Board:    s.board,
				Mark:     s.mark,
				Deadline: t.Add(time.Duration(s.deadlineRemainingTicks/tickRate) * time.Second).Unix(),
			}
		} else if s.finished && s.board != nil {
			opCode = api.OpCode_OPCODE_DONE
			msg = &api.Done{
				Board:           s.board,
				Winner:          s.winner,
				WinnerPositions: s.winnerPositions,
				NextGameStart:   0,
			}
		}

		if msg != nil {
			buf, err := m.marshaler.Marshal(msg)
			if err != nil {
				logger.Error("error encoding message: %v", err)
			} else {
				_ = dispatcher.BroadcastMessage(int64(opCode), buf, []runtime.Presence{presence}, nil, true)
			}
		}
	}

	if len(s.presences) >= 2 && s.label.Open != 0 {
		s.label.Open = 0
		if labelJSON, err := json.Marshal(s.label); err != nil {
			logger.Error("error encoding label: %v", err)
		} else if err := dispatcher.MatchLabelUpdate(string(labelJSON)); err != nil {
			logger.Error("error updating label: %v", err)
		}
	}

	return s
}

func (m *MatchHandler) MatchLeave(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, dispatcher runtime.MatchDispatcher, tick int64, state interface{}, presences []runtime.Presence) interface{} {
	s := state.(*MatchState)

	for _, presence := range presences {
		userID := presence.GetUserId()
		s.presences[userID] = nil
		if s.playing {
			if _, ok := s.marks[userID]; ok {
				s.disconnectTicks[userID] = 0
			}
		}
	}

	remaining := make([]runtime.Presence, 0, 1)
	for _, p := range s.presences {
		if p != nil {
			remaining = append(remaining, p)
		}
	}
	if len(remaining) == 1 && s.playing {
		_ = dispatcher.BroadcastMessage(int64(api.OpCode_OPCODE_OPPONENT_LEFT), nil, remaining, nil, true)
	}

	return s
}

func (m *MatchHandler) MatchLoop(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, dispatcher runtime.MatchDispatcher, tick int64, state interface{}, messages []runtime.MatchData) interface{} {
	s := state.(*MatchState)

	if s.ConnectedCount()+s.joinsInProgress == 0 {
		s.emptyTicks++
		if s.emptyTicks >= maxEmptySec*tickRate {
			logger.Info("closing idle match")
			return nil
		}
	}

	if s.finished {
		s.finishRemainingTicks--
		if s.finishRemainingTicks <= 0 {
			return nil
		}
		return s
	}

	if s.pendingCloseRoomUserID != "" {
		requestUserID := s.pendingCloseRoomUserID
		s.pendingCloseRoomUserID = ""
		if _, ok := s.presences[requestUserID]; ok && s.ConnectedCount() <= 1 {
			s.finished = true
			s.finishRemainingTicks = 1
			return s
		}
	}

	if !s.playing {
		for userID, presence := range s.presences {
			if presence == nil {
				delete(s.presences, userID)
				delete(s.disconnectTicks, userID)
			}
		}

		if len(s.presences) < 2 && s.label.Open != 1 {
			s.label.Open = 1
			if labelJSON, err := json.Marshal(s.label); err != nil {
				logger.Error("error encoding label: %v", err)
			} else if err := dispatcher.MatchLabelUpdate(string(labelJSON)); err != nil {
				logger.Error("error updating label: %v", err)
			}
		}

		if s.ConnectedCount() < 2 {
			return s
		}

		m.startGame(s, dispatcher, logger)
		return s
	}

	if s.pendingSurrenderUserID != "" {
		loserID := s.pendingSurrenderUserID
		s.pendingSurrenderUserID = ""
		if loserMark, ok := s.marks[loserID]; ok {
			m.finishGame(ctx, nk, s, oppositeMark(loserMark), nil, dispatcher, logger)
			return s
		}
	}

	for _, message := range messages {
		senderMark, senderOk := s.marks[message.GetUserId()]
		if !senderOk {
			_ = dispatcher.BroadcastMessage(int64(api.OpCode_OPCODE_REJECTED), nil, []runtime.Presence{message}, nil, true)
			continue
		}

		switch api.OpCode(message.GetOpCode()) {
		case api.OpCode_OPCODE_MOVE:
			if s.mark != senderMark {
				_ = dispatcher.BroadcastMessage(int64(api.OpCode_OPCODE_REJECTED), nil, []runtime.Presence{message}, nil, true)
				continue
			}

			move := &api.Move{}
			if err := m.unmarshaler.Unmarshal(message.GetData(), move); err != nil {
				_ = dispatcher.BroadcastMessage(int64(api.OpCode_OPCODE_REJECTED), nil, []runtime.Presence{message}, nil, true)
				continue
			}

			if move.Position < 0 || move.Position > 8 || s.board[move.Position] != api.Mark_MARK_UNSPECIFIED {
				_ = dispatcher.BroadcastMessage(int64(api.OpCode_OPCODE_REJECTED), nil, []runtime.Presence{message}, nil, true)
				continue
			}

			s.board[move.Position] = senderMark
			s.mark = oppositeMark(senderMark)
			s.deadlineRemainingTicks = calculateDeadlineTicks(s.label)

			winner, winnerPos := findWinner(s.board)
			if winner != api.Mark_MARK_UNSPECIFIED {
				m.finishGame(ctx, nk, s, winner, winnerPos, dispatcher, logger)
				return s
			}
			if isTie(s.board) {
				m.finishGame(ctx, nk, s, api.Mark_MARK_UNSPECIFIED, nil, dispatcher, logger)
				return s
			}

			m.broadcastUpdate(s, dispatcher, logger)

		default:
			_ = dispatcher.BroadcastMessage(int64(api.OpCode_OPCODE_REJECTED), nil, []runtime.Presence{message}, nil, true)
		}
	}

	for userID, playerMark := range s.marks {
		if s.presences[userID] == nil {
			s.disconnectTicks[userID]++
			if s.disconnectTicks[userID] >= int64(turnTimeSec*tickRate) {
				m.finishGame(ctx, nk, s, oppositeMark(playerMark), nil, dispatcher, logger)
				return s
			}
		} else {
			s.disconnectTicks[userID] = 0
		}
	}

	s.deadlineRemainingTicks--
	if s.deadlineRemainingTicks <= 0 {
		m.finishGame(ctx, nk, s, oppositeMark(s.mark), nil, dispatcher, logger)
		return s
	}

	return s
}

func (m *MatchHandler) MatchSignal(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, dispatcher runtime.MatchDispatcher, tick int64, state interface{}, data string) (interface{}, string) {
	s := state.(*MatchState)

	if strings.HasPrefix(data, "surrender:") {
		if !s.playing {
			return s, "match not in progress"
		}
		userID := strings.TrimSpace(strings.TrimPrefix(data, "surrender:"))
		if userID == "" {
			return s, "invalid surrender signal"
		}
		if _, ok := s.marks[userID]; !ok {
			return s, "user not in match"
		}

		s.pendingSurrenderUserID = userID
		return s, "ok"
	}

	if strings.HasPrefix(data, "close_room:") {
		userID := strings.TrimSpace(strings.TrimPrefix(data, "close_room:"))
		if userID == "" {
			return s, "invalid close_room signal"
		}
		s.pendingCloseRoomUserID = userID
		return s, "ok"
	}

	return s, "unsupported signal"
}

func (m *MatchHandler) MatchTerminate(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, dispatcher runtime.MatchDispatcher, tick int64, state interface{}, graceSeconds int) interface{} {
	return state
}

func (m *MatchHandler) startGame(s *MatchState, dispatcher runtime.MatchDispatcher, logger runtime.Logger) {
	userIDs := make([]string, 0, 2)
	for userID, p := range s.presences {
		if p != nil {
			userIDs = append(userIDs, userID)
		}
	}
	sort.Strings(userIDs)
	if len(userIDs) != 2 {
		return
	}

	s.playing = true
	s.board = make([]api.Mark, 9)
	s.marks = map[string]api.Mark{
		userIDs[0]: api.Mark_MARK_X,
		userIDs[1]: api.Mark_MARK_O,
	}
	s.mark = api.Mark_MARK_X
	s.winner = api.Mark_MARK_UNSPECIFIED
	s.winnerPositions = nil
	s.deadlineRemainingTicks = calculateDeadlineTicks(s.label)
	for _, userID := range userIDs {
		s.disconnectTicks[userID] = 0
	}

	t := time.Now().UTC()
	buf, err := m.marshaler.Marshal(&api.Start{
		Board:    s.board,
		Marks:    s.marks,
		Mark:     s.mark,
		Deadline: t.Add(time.Duration(s.deadlineRemainingTicks/tickRate) * time.Second).Unix(),
	})
	if err != nil {
		logger.Error("error encoding message: %v", err)
		return
	}
	_ = dispatcher.BroadcastMessage(int64(api.OpCode_OPCODE_START), buf, nil, nil, true)
}

func (m *MatchHandler) finishGame(ctx context.Context, nk runtime.NakamaModule, s *MatchState, winner api.Mark, winnerPositions []int32, dispatcher runtime.MatchDispatcher, logger runtime.Logger) {
	s.playing = false
	s.finished = true
	s.finishRemainingTicks = matchEndGraceSec * tickRate
	s.deadlineRemainingTicks = 0
	s.winner = winner
	s.winnerPositions = winnerPositions

	m.updateProfileStats(ctx, nk, s, winner, logger)

	buf, err := m.marshaler.Marshal(&api.Done{
		Board:           s.board,
		Winner:          s.winner,
		WinnerPositions: s.winnerPositions,
		NextGameStart:   0,
	})
	if err != nil {
		logger.Error("error encoding message: %v", err)
		return
	}
	_ = dispatcher.BroadcastMessage(int64(api.OpCode_OPCODE_DONE), buf, nil, nil, true)
}

func (m *MatchHandler) updateProfileStats(ctx context.Context, nk runtime.NakamaModule, s *MatchState, winner api.Mark, logger runtime.Logger) {
	xUserID := ""
	oUserID := ""
	for userID, mark := range s.marks {
		switch mark {
		case api.Mark_MARK_X:
			xUserID = userID
		case api.Mark_MARK_O:
			oUserID = userID
		}
	}

	if xUserID == "" || oUserID == "" {
		return
	}

	if winner == api.Mark_MARK_UNSPECIFIED {
		if err := updateUserProfileStat(ctx, nk, xUserID, "draws"); err != nil {
			logger.Error("failed to update draws for %s: %v", xUserID, err)
		}
		if err := updateUserProfileStat(ctx, nk, oUserID, "draws"); err != nil {
			logger.Error("failed to update draws for %s: %v", oUserID, err)
		}
		return
	}

	if winner == api.Mark_MARK_X {
		if err := updateUserProfileStat(ctx, nk, xUserID, "wins"); err != nil {
			logger.Error("failed to update wins for %s: %v", xUserID, err)
		}
		if err := updateUserProfileStat(ctx, nk, oUserID, "losses"); err != nil {
			logger.Error("failed to update losses for %s: %v", oUserID, err)
		}
		return
	}

	if err := updateUserProfileStat(ctx, nk, oUserID, "wins"); err != nil {
		logger.Error("failed to update wins for %s: %v", oUserID, err)
	}
	if err := updateUserProfileStat(ctx, nk, xUserID, "losses"); err != nil {
		logger.Error("failed to update losses for %s: %v", xUserID, err)
	}
}

func (m *MatchHandler) broadcastUpdate(s *MatchState, dispatcher runtime.MatchDispatcher, logger runtime.Logger) {
	t := time.Now().UTC()
	buf, err := m.marshaler.Marshal(&api.Update{
		Board:    s.board,
		Mark:     s.mark,
		Deadline: t.Add(time.Duration(s.deadlineRemainingTicks/tickRate) * time.Second).Unix(),
	})
	if err != nil {
		logger.Error("error encoding message: %v", err)
		return
	}
	_ = dispatcher.BroadcastMessage(int64(api.OpCode_OPCODE_UPDATE), buf, nil, nil, true)
}

func findWinner(board []api.Mark) (api.Mark, []int32) {
winCheck:
	for _, winningPosition := range winningPositions {
		mark := board[winningPosition[0]]
		if mark == api.Mark_MARK_UNSPECIFIED {
			continue
		}
		for _, position := range winningPosition {
			if board[position] != mark {
				continue winCheck
			}
		}
		return mark, winningPosition
	}
	return api.Mark_MARK_UNSPECIFIED, nil
}

func isTie(board []api.Mark) bool {
	for _, mark := range board {
		if mark == api.Mark_MARK_UNSPECIFIED {
			return false
		}
	}
	return true
}

func oppositeMark(mark api.Mark) api.Mark {
	switch mark {
	case api.Mark_MARK_X:
		return api.Mark_MARK_O
	case api.Mark_MARK_O:
		return api.Mark_MARK_X
	default:
		return api.Mark_MARK_UNSPECIFIED
	}
}

func calculateDeadlineTicks(l *MatchLabel) int64 {
	_ = l
	return turnTimeSec * tickRate
}
