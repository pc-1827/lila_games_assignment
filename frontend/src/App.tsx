import { useEffect, useMemo, useState } from "react"
import type { Session, Socket } from "@heroiclabs/nakama-js"
import "./App.css"
import {
  checkBackendHealth,
  connectSocket,
  nakamaClient,
  parseRpcPayload,
} from "./nakama"
import {
  Mark,
  OpCode,
  type DonePayload,
  type RoomInfo,
  type RpcCreateRoomResponse,
  type RpcFindMatchResponse,
  type RpcJoinRoomResponse,
  type RpcListRoomsResponse,
  type StartPayload,
  type UpdatePayload,
} from "./types"

type Phase = "auth" | "lobby" | "match"
type AuthMode = "login" | "signup"

const emptyBoard: Mark[] = Array(9).fill(Mark.MARK_UNSPECIFIED)

function markToText(mark: Mark) {
  if (mark === Mark.MARK_X) return "X"
  if (mark === Mark.MARK_O) return "O"
  return ""
}

function shortId(id?: string) {
  if (!id) return ""
  if (id.length < 10) return id
  return `${id.slice(0, 6)}...${id.slice(-4)}`
}

function decodeMatchData(data: string | ArrayBuffer | Uint8Array): string {
  if (typeof data === "string") return data
  if (data instanceof ArrayBuffer) return new TextDecoder().decode(new Uint8Array(data))
  return new TextDecoder().decode(data)
}

function toMark(value: unknown): Mark {
  if (typeof value === "number") return value as Mark
  if (typeof value === "string") {
    if (value === "MARK_X") return Mark.MARK_X
    if (value === "MARK_O") return Mark.MARK_O
  }
  return Mark.MARK_UNSPECIFIED
}

function toMarksMap(input: Record<string, unknown> | undefined): Record<string, Mark> {
  const out: Record<string, Mark> = {}
  if (!input) return out
  for (const [k, v] of Object.entries(input)) {
    out[k] = toMark(v)
  }
  return out
}

async function getErrorMessage(err: unknown): Promise<string> {
  if (!err) return "Unknown error"

  if (err instanceof Response) {
    try {
      const text = await err.text()
      if (!text) return `Request failed (${err.status})`
      try {
        const parsed = JSON.parse(text) as { message?: string; error?: { Message?: string } }
        if (parsed?.error?.Message) return parsed.error.Message
        if (parsed?.message) return parsed.message
      } catch {
        // not json
      }
      return text
    } catch {
      return `Request failed (${err.status})`
    }
  }

  const anyErr = err as { message?: unknown }
  if (typeof anyErr.message === "string") {
    try {
      const parsed = JSON.parse(anyErr.message) as { message?: string; error?: { Message?: string } }
      if (parsed?.error?.Message) return parsed.error.Message
      if (parsed?.message) return parsed.message
    } catch {
      // noop
    }
    return anyErr.message
  }
  return String(err)
}

export default function App() {
  const [phase, setPhase] = useState<Phase>("auth")
  const [authMode, setAuthMode] = useState<AuthMode>("login")
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [isAuthenticating, setIsAuthenticating] = useState(false)

  const [session, setSession] = useState<Session | null>(null)
  const [socket, setSocket] = useState<Socket | null>(null)

  const [rooms, setRooms] = useState<RoomInfo[]>([])
  const [currentMatchId, setCurrentMatchId] = useState<string>("")

  const [board, setBoard] = useState<Mark[]>(emptyBoard)
  const [marksByUser, setMarksByUser] = useState<Record<string, Mark>>({})
  const [turn, setTurn] = useState<Mark>(Mark.MARK_UNSPECIFIED)
  const [winner, setWinner] = useState<Mark>(Mark.MARK_UNSPECIFIED)
  const [done, setDone] = useState(false)
  const [deadlineUnix, setDeadlineUnix] = useState<number>(0)
  const [nowUnix, setNowUnix] = useState<number>(Math.floor(Date.now() / 1000))
  const [statusText, setStatusText] = useState("")
  const [profile, setProfile] = useState<{ wins: number; losses: number; draws: number }>({
    wins: 0,
    losses: 0,
    draws: 0,
  })
  const [presentUserIds, setPresentUserIds] = useState<string[]>([])
  const [backendHealthy, setBackendHealthy] = useState(false)
  const [isCheckingBackend, setIsCheckingBackend] = useState(true)

  const myUserId = session?.user_id ?? ""
  const myMark = (myUserId && marksByUser[myUserId]) || Mark.MARK_UNSPECIFIED
  const myTurn = myMark !== Mark.MARK_UNSPECIFIED && myMark === turn && !done
  const connectedPlayers = presentUserIds.length
  const disableBackToLobby = !done && connectedPlayers >= 2
  const disableSurrender = done || connectedPlayers <= 1

  const timeLeft = useMemo(() => {
    if (!deadlineUnix || done) return 0
    return Math.max(0, deadlineUnix - nowUnix)
  }, [deadlineUnix, done, nowUnix])

  useEffect(() => {
    if (!socket) return

    socket.onmatchdata = (matchData) => {
      const raw = decodeMatchData(matchData.data)
      let parsed: StartPayload | UpdatePayload | DonePayload | null = null

      try {
        parsed = JSON.parse(raw)
      } catch {
        setStatusText("Invalid data received from server.")
        return
      }

      switch (matchData.op_code) {
        case OpCode.OPCODE_START: {
          const p = parsed as StartPayload
          setBoard((p.board || []).map((x) => toMark(x)) as Mark[])
          setMarksByUser(toMarksMap((p as unknown as { marks?: Record<string, unknown> }).marks))
          setTurn(toMark(p.mark))
          setDeadlineUnix(p.deadline || 0)
          setWinner(Mark.MARK_UNSPECIFIED)
          setDone(false)
          setStatusText("Match started.")
          setPhase("match")
          break
        }
        case OpCode.OPCODE_UPDATE: {
          const p = parsed as UpdatePayload
          setBoard((p.board || []).map((x) => toMark(x)) as Mark[])
          setTurn(toMark(p.mark))
          setDeadlineUnix(p.deadline || 0)
          setStatusText("Move accepted.")
          break
        }
        case OpCode.OPCODE_DONE: {
          const p = parsed as DonePayload
          setBoard((p.board || []).map((x) => toMark(x)) as Mark[])
          setWinner(toMark(p.winner))
          setDone(true)
          setDeadlineUnix(0)
          setStatusText("Match finished.")
          break
        }
        case OpCode.OPCODE_REJECTED: {
          setStatusText("Move rejected by server.")
          break
        }
        case OpCode.OPCODE_OPPONENT_LEFT: {
          setStatusText("Opponent disconnected. Waiting up to 30 seconds.")
          break
        }
        default: {
          setStatusText(`Unknown op code ${matchData.op_code}.`)
        }
      }
    }

    socket.onmatchpresence = (presenceEvent) => {
      const joins = presenceEvent.joins?.length ?? 0
      const leaves = presenceEvent.leaves?.length ?? 0
      setPresentUserIds((prev) => {
        const set = new Set(prev)
        for (const p of presenceEvent.joins || []) {
          set.add(p.user_id)
        }
        for (const p of presenceEvent.leaves || []) {
          set.delete(p.user_id)
        }
        return Array.from(set)
      })
      if (joins > 0) setStatusText("A player joined the match.")
      if (leaves > 0) setStatusText("A player left the match.")
    }

    socket.ondisconnect = () => {
      setStatusText("Socket disconnected.")
    }
  }, [socket])

  useEffect(() => {
    if (!deadlineUnix || done) return
    const id = window.setInterval(() => {
      setNowUnix(Math.floor(Date.now() / 1000))
    }, 1000)
    return () => window.clearInterval(id)
  }, [deadlineUnix, done])

  useEffect(() => {
    let mounted = true

    const runHealthCheck = async () => {
      const ok = await checkBackendHealth()
      if (!mounted) return
      setBackendHealthy(ok)
      setIsCheckingBackend(false)
    }

    void runHealthCheck()
    const id = window.setInterval(() => {
      void runHealthCheck()
    }, 5000)

    return () => {
      mounted = false
      window.clearInterval(id)
    }
  }, [])

  const loadProfile = async (authSession = session) => {
    if (!authSession) return
    try {
      const res = await nakamaClient.rpc(authSession, "get_profile", {})
      const payload = parseRpcPayload<{ wins: number; losses: number; draws: number }>(res.payload)
      setProfile({
        wins: payload.wins ?? 0,
        losses: payload.losses ?? 0,
        draws: payload.draws ?? 0,
      })
    } catch (err) {
      console.error(err)
      setStatusText("Could not load profile.")
    }
  }

  const authenticate = async () => {
    setIsAuthenticating(true)
    try {
      const uname = username.trim().toLowerCase()
      if (!uname || !password) {
        setStatusText("Username and password are required.")
        setIsAuthenticating(false)
        return
      }

      const email = `${uname}@ttt.local`
      const create = authMode === "signup"
      const s = await nakamaClient.authenticateEmail(email, password, create, create ? uname : undefined)
      const sk = await connectSocket(s)
      setSession(s)
      setSocket(sk)
      setStatusText("Authenticated and connected.")
      setPhase("lobby")
      await loadProfile(s)
      await loadRooms(s)
    } catch (err) {
      console.error(err)
      const msg = await getErrorMessage(err)
      if (msg.toLowerCase().includes("password") && msg.includes("8")) {
        setStatusText(msg)
        return
      }
      if (authMode === "signup" && (msg.includes("already") || msg.includes("exists"))) {
        setStatusText("Username already exists. Choose another one.")
      } else if (authMode === "login") {
        setStatusText(msg || "Login failed. Check username/password.")
      } else {
        setStatusText(msg || "Authentication failed. Check Nakama server is running.")
      }
    } finally {
      setIsAuthenticating(false)
    }
  }

  const loadRooms = async (authSession = session) => {
    if (!authSession) return
    try {
      const res = await nakamaClient.rpc(
        authSession,
        "list_rooms",
        { limit: 20 }
      )
      const payload = parseRpcPayload<RpcListRoomsResponse>(res.payload)
      setRooms(payload.rooms || [])
    } catch (err) {
      console.error(err)
      const msg = await getErrorMessage(err)
      setStatusText(msg || "Could not list rooms.")
    }
  }

  const joinMatch = async (matchId: string) => {
    if (!socket || !session) return
    try {
      const roomCheck = await nakamaClient.rpc(
        session,
        "join_room",
        { match_id: matchId }
      )
      const joinPayload = parseRpcPayload<RpcJoinRoomResponse>(roomCheck.payload)
      if (!joinPayload.joinable) {
        setStatusText("Room is not joinable.")
        return
      }

      const match = await socket.joinMatch(matchId)
      const initialPresence = new Set<string>((match.presences || []).map((p) => p.user_id))
      if (match.self?.user_id) initialPresence.add(match.self.user_id)
      setPresentUserIds(Array.from(initialPresence))
      setCurrentMatchId(matchId)
      setStatusText("Joined room. Waiting for match start.")
      setPhase("match")
      setDone(false)
      setWinner(Mark.MARK_UNSPECIFIED)
      setBoard(emptyBoard)
      setMarksByUser({})
      setTurn(Mark.MARK_UNSPECIFIED)
      setDeadlineUnix(0)
    } catch (err) {
      console.error(err)
      const msg = await getErrorMessage(err)
      setStatusText(msg || "Failed to join room.")
    }
  }

  const findMatch = async () => {
    if (!session) return
    try {
      const res = await nakamaClient.rpc(
        session,
        "find_match",
        { fast: false }
      )
      const payload = parseRpcPayload<RpcFindMatchResponse>(res.payload)
      const payloadAny = payload as RpcFindMatchResponse & { matchIds?: string[] }
      const first = payload.match_ids?.[0] ?? payloadAny.matchIds?.[0]
      if (!first) {
        setStatusText("No open room found. Please create a room.")
        return
      }
      await joinMatch(first)
    } catch (err) {
      console.error(err)
      const msg = await getErrorMessage(err)
      setStatusText(msg || "Automatic matchmaking failed.")
    }
  }

  const createRoom = async () => {
    if (!session) return
    try {
      const res = await nakamaClient.rpc(
        session,
        "create_room",
        { fast: false }
      )
      const payload = parseRpcPayload<RpcCreateRoomResponse>(res.payload)
      if (!payload.match_id) {
        setStatusText("Invalid create room response.")
        return
      }
      await loadRooms(session)
      await joinMatch(payload.match_id)
    } catch (err) {
      console.error(err)
      const msg = await getErrorMessage(err)
      setStatusText(msg || "Create room failed.")
    }
  }

  const makeMove = async (idx: number) => {
    if (!socket || !currentMatchId || !myTurn || board[idx] !== Mark.MARK_UNSPECIFIED) return
    try {
      await socket.sendMatchState(
        currentMatchId,
        OpCode.OPCODE_MOVE,
        JSON.stringify({ position: idx })
      )
    } catch (err) {
      console.error(err)
      setStatusText("Failed to send move.")
    }
  }

  const surrender = async () => {
    if (!session || !currentMatchId) return
    try {
      await nakamaClient.rpc(
        session,
        "surrender",
        { match_id: currentMatchId }
      )
      setStatusText("Surrender request sent.")
    } catch (err) {
      console.error(err)
      const msg = await getErrorMessage(err)
      setStatusText(msg || "Surrender failed.")
    }
  }

  const backToLobby = async () => {
    if (disableBackToLobby) {
      return
    }

    const leavingMatchId = currentMatchId

    if (session && currentMatchId && !done) {
      try {
        await nakamaClient.rpc(session, "close_room", { match_id: currentMatchId })
      } catch {
        // noop
      }
    }

    if (socket && currentMatchId) {
      try {
        await socket.leaveMatch(currentMatchId)
      } catch {
        // noop
      }
    }

    if (leavingMatchId) {
      setRooms((prev) => prev.filter((room) => room.match_id !== leavingMatchId))
    }

    setCurrentMatchId("")
    setBoard(emptyBoard)
    setMarksByUser({})
    setTurn(Mark.MARK_UNSPECIFIED)
    setWinner(Mark.MARK_UNSPECIFIED)
    setDone(false)
    setDeadlineUnix(0)
    setPresentUserIds([])
    setPhase("lobby")
    await loadProfile()
    await loadRooms()

    // Render may still return a room briefly while the authoritative match shuts down.
    if (leavingMatchId) {
      window.setTimeout(() => {
        void loadRooms()
      }, 1200)
    }
  }

  const winnerText = (() => {
    if (!done) return ""
    if (winner === Mark.MARK_UNSPECIFIED) return "Draw"
    if (winner === myMark) return "You won"
    return "You lost"
  })()

  return (
    <div className="app">
      <header className="topbar">
        <h1>Tic-Tac-Toe</h1>
        <p>Server authoritative multiplayer</p>
      </header>

      {phase === "auth" && (
        <section className="panel auth-panel">
          <h2>{authMode === "login" ? "Login" : "Sign Up"}</h2>
          <div className="auth-switch">
            <button onClick={() => setAuthMode("login")} disabled={authMode === "login" || isAuthenticating}>Login</button>
            <button onClick={() => setAuthMode("signup")} disabled={authMode === "signup" || isAuthenticating}>Sign Up</button>
          </div>
          <input
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            placeholder="Username"
            maxLength={24}
            disabled={isAuthenticating}
          />
          <input
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            type="password"
            placeholder="Password"
            maxLength={64}
            disabled={isAuthenticating}
          />
          <button onClick={authenticate} disabled={isAuthenticating}>
            {isAuthenticating ? "Please wait..." : (authMode === "signup" ? "Create Account" : "Login")}
          </button>
        </section>
      )}

      {phase === "lobby" && (
        <section className="panel">
          <div className="profile">
            <h3>Profile</h3>
            <div className="profile-stats">
              <span>Wins: {profile.wins}</span>
              <span>Losses: {profile.losses}</span>
              <span>Draws: {profile.draws}</span>
            </div>
          </div>
          <div className="lobby-actions">
            <button onClick={findMatch}>Auto Match</button>
            <button onClick={createRoom}>Create Room</button>
            <button onClick={() => loadRooms()}>Refresh Rooms</button>
          </div>

          <h2>Open Rooms</h2>
          {rooms.length === 0 ? (
            <p className="muted">No open rooms right now.</p>
          ) : (
            <ul className="rooms">
              {rooms.map((room) => (
                <li key={room.match_id}>
                  <div>
                    <div className="room-id">{shortId(room.match_id)}</div>
                    <div className="muted">
                      Players {room.size}/{room.max_size}
                    </div>
                  </div>
                  <button onClick={() => joinMatch(room.match_id)}>Join</button>
                </li>
              ))}
            </ul>
          )}
        </section>
      )}

      {phase === "match" && (
        <section className="panel">
          <div className="match-meta">
            <div>Match {shortId(currentMatchId)}</div>
            <div>You are {markToText(myMark) || "-"}</div>
            <div>Turn {markToText(turn) || "-"}</div>
            <div>Timer {timeLeft}s</div>
            <div>Players {connectedPlayers}</div>
          </div>

          <div className="board">
            {board.map((cell, i) => (
              <button
                key={i}
                className={`cell ${myTurn && cell === Mark.MARK_UNSPECIFIED ? "active" : ""}`}
                disabled={!myTurn || cell !== Mark.MARK_UNSPECIFIED || done}
                onClick={() => makeMove(i)}
              >
                {markToText(cell)}
              </button>
            ))}
          </div>

          <div className="match-actions">
            <button onClick={surrender} disabled={disableSurrender}>
              Surrender
            </button>
            <button onClick={backToLobby} disabled={disableBackToLobby}>Back to Lobby</button>
          </div>

          {done && <h3 className="result">{winnerText}</h3>}
        </section>
      )}

      <footer className="status">
        <span className={`backend-state ${backendHealthy ? "ok" : "down"}`}>
          Backend: {isCheckingBackend ? "Checking..." : (backendHealthy ? "Online" : "Starting / Unavailable")}
        </span>
        {statusText ? <span className="ui-state"> | {statusText}</span> : null}
      </footer>
    </div>
  )
}