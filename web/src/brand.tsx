// Brand mark reused in the sidebar and login. Matches public/favicon.svg.
export function Logo({ size = 32 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 64 64" fill="none" xmlns="http://www.w3.org/2000/svg" aria-label="FRPanel">
      <rect width="64" height="64" rx="16" fill="#2065D1" />
      <path d="M20 32c0-6.627 5.373-12 12-12s12 5.373 12 12" stroke="#fff" strokeWidth="4" strokeLinecap="round" />
      <circle cx="20" cy="32" r="5" fill="#fff" />
      <circle cx="44" cy="32" r="5" fill="#fff" />
      <circle cx="32" cy="46" r="5" fill="#BBD3FF" />
      <path d="M20 34.5v4a12 12 0 0 0 12 7.5m24-11.5v4a12 12 0 0 1-12 7.5" stroke="#BBD3FF" strokeWidth="3" strokeLinecap="round" />
    </svg>
  )
}

export const BRAND_NAME = 'FRPanel'
