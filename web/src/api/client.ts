import axios, { type AxiosRequestConfig } from 'axios'

export class ApiError extends Error {
  code: number
  constructor(code: number, message: string) {
    super(message)
    this.code = code
  }
}

function getCookie(name: string): string {
  const m = document.cookie.match(new RegExp('(?:^|; )' + name + '=([^;]*)'))
  return m ? decodeURIComponent(m[1]) : ''
}

export const http = axios.create({ baseURL: '/api', withCredentials: true, timeout: 20000 })

// Attach the double-submit CSRF token on mutating requests.
http.interceptors.request.use((cfg) => {
  const method = (cfg.method || 'get').toLowerCase()
  if (!['get', 'head', 'options'].includes(method)) {
    const csrf = getCookie('frpanel_csrf')
    if (csrf) cfg.headers.set('X-CSRF-Token', csrf)
  }
  return cfg
})

// Unwrap the {code,message,data} envelope; surface business errors as ApiError.
http.interceptors.response.use(
  (resp) => {
    const body = resp.data
    if (body && typeof body === 'object' && 'code' in body) {
      if (body.code === 0) return body.data
      throw new ApiError(body.code, body.message || '请求失败')
    }
    return body
  },
  (err) => {
    const body = err.response?.data
    if (body && typeof body === 'object' && 'code' in body) {
      const e = new ApiError(body.code, body.message || '请求失败')
      if (body.code === 40101 || body.code === 40102) {
        window.dispatchEvent(new CustomEvent('frpanel-unauthorized'))
      }
      throw e
    }
    throw new ApiError(0, err.message || '网络错误，请检查连接')
  },
)

export function apiGet<T>(url: string, config?: AxiosRequestConfig): Promise<T> {
  return http.get(url, config) as unknown as Promise<T>
}
export function apiPost<T>(url: string, data?: unknown, config?: AxiosRequestConfig): Promise<T> {
  return http.post(url, data, config) as unknown as Promise<T>
}
export function apiPut<T>(url: string, data?: unknown): Promise<T> {
  return http.put(url, data) as unknown as Promise<T>
}
export function apiDelete<T>(url: string): Promise<T> {
  return http.delete(url) as unknown as Promise<T>
}
