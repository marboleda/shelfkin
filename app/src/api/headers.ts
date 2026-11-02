/** Extra headers to send with an API request. */
export type RequestHeaders = Record<string, string>;

/**
 * The one place the app adds headers to API requests. The client calls it
 * before every request and sets each returned header.
 *
 * It adds nothing yet. Sign-in (#25) will return the Firebase ID token here as
 * `Authorization: Bearer <token>`.
 */
export async function getRequestHeaders(): Promise<RequestHeaders> {
  return {};
}
