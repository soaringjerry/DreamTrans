export const DREAMTRANS_WEBSOCKET_PROTOCOL = 'dreamtrans.v1'

export function websocketAuthProtocols(token: string): readonly string[] {
  return [
    DREAMTRANS_WEBSOCKET_PROTOCOL,
    `dreamtrans.jwt.${token}`,
  ]
}
