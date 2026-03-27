export enum OpCode {
  OPCODE_UNSPECIFIED = 0,
  OPCODE_START = 1,
  OPCODE_UPDATE = 2,
  OPCODE_DONE = 3,
  OPCODE_MOVE = 4,
  OPCODE_REJECTED = 5,
  OPCODE_OPPONENT_LEFT = 6,
}

export enum Mark {
  MARK_UNSPECIFIED = 0,
  MARK_X = 1,
  MARK_O = 2,
}

export type StartPayload = {
  board: Mark[]
  marks: Record<string, Mark>
  mark: Mark
  deadline: number
}

export type UpdatePayload = {
  board: Mark[]
  mark: Mark
  deadline: number
}

export type DonePayload = {
  board: Mark[]
  winner: Mark
  winner_positions: number[]
  next_game_start: number
}

export type RpcFindMatchResponse = {
  match_ids: string[]
}

export type RpcCreateRoomResponse = {
  match_id: string
}

export type RpcJoinRoomResponse = {
  match_id: string
  joinable: boolean
}

export type RoomInfo = {
  match_id: string
  size: number
  max_size: number
  label: string
  is_open: boolean
  fast_mode: boolean
}

export type RpcListRoomsResponse = {
  rooms: RoomInfo[]
}

export type UserProfile = {
  wins: number
  losses: number
  draws: number
}