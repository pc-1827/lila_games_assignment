import { Client, Session, type Socket } from "@heroiclabs/nakama-js"

const SERVER_KEY = "defaultkey"
const HTTP_KEY = import.meta.env.VITE_NAKAMA_HTTP_KEY || "defaulthttpkey"
const HOST = import.meta.env.VITE_NAKAMA_HOST || "127.0.0.1"
const PORT = import.meta.env.VITE_NAKAMA_PORT || "7350"
const USE_SSL = import.meta.env.VITE_NAKAMA_USE_SSL === "true"

const HEALTH_TIMEOUT_MS = 5000

export const nakamaClient = new Client(SERVER_KEY, HOST, PORT, USE_SSL)

export const createSocket = () => nakamaClient.createSocket(USE_SSL, false)

export const parseRpcPayload = <T>(payload: string | object | undefined | null): T => {
    if (!payload) return {} as T
    if (typeof payload === "string") {
        return JSON.parse(payload) as T
    }
    return payload as T
}

export const authWithDevice = async (username: string) => {
    const deviceKey = "ttt_device_id"
    let deviceId = localStorage.getItem(deviceKey)
    if (!deviceId) {
        deviceId = crypto.randomUUID()
        localStorage.setItem(deviceKey, deviceId)
    }

    const session = await nakamaClient.authenticateDevice(
        deviceId,
        true,
        username || undefined
    )

    return session
}

export const connectSocket = async (session: Session): Promise<Socket> => {
    const socket = createSocket()
    await socket.connect(session, true)
    return socket
}

export const getNakamaBaseUrl = () => `${USE_SSL ? "https" : "http"}://${HOST}:${PORT}`

export const checkBackendHealth = async (): Promise<boolean> => {
    const controller = new AbortController()
    const timeoutId = window.setTimeout(() => controller.abort(), HEALTH_TIMEOUT_MS)

    try {
        const response = await fetch(`${getNakamaBaseUrl()}/healthcheck`, {
            method: "GET",
            headers: {
                Authorization: `Basic ${btoa(`${HTTP_KEY}:`)}`,
            },
            signal: controller.signal,
        })
        return response.ok
    } catch {
        return false
    } finally {
        window.clearTimeout(timeoutId)
    }
}