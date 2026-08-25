import '@fontsource/public-sans/400.css'
import '@fontsource/public-sans/500.css'
import '@fontsource/public-sans/700.css'
import React from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'
import { ColorModeProvider } from './ColorMode'
import { ToastProvider } from './components/Toast'
import { ErrorBoundary } from './components/ErrorBoundary'
import App from './App'

const qc = new QueryClient({
  defaultOptions: { queries: { retry: 1, refetchOnWindowFocus: false, staleTime: 5000 } },
})

createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <ColorModeProvider>
      <ErrorBoundary>
        <QueryClientProvider client={qc}>
          <BrowserRouter>
            <ToastProvider>
              <App />
            </ToastProvider>
          </BrowserRouter>
        </QueryClientProvider>
      </ErrorBoundary>
    </ColorModeProvider>
  </React.StrictMode>,
)
