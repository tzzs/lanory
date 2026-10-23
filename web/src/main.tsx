import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { App } from './app/App'
import './index.css'
import { initializeEmbeddedSession } from '@/lib/embed'
import { t } from '@/i18n'

const root = document.getElementById('root')
if (!root) throw new Error('missing #root element')

const appRoot = createRoot(root)
void initializeEmbeddedSession().then(() => {
  appRoot.render(<StrictMode><App /></StrictMode>)
}).catch(() => {
  appRoot.render(
    <div className="flex h-full items-center justify-center p-6">
      <div className="max-w-md space-y-3 text-sm" role="alert">
        <h1 className="font-semibold">{t('app.session_unavailable_title')}</h1>
        <p>{t('app.session_unavailable_body')}</p>
        <button className="rounded border px-3 py-2" onClick={() => window.location.reload()}>{t('app.session_reload')}</button>
      </div>
    </div>,
  )
})
