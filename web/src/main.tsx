import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { App } from './app/App'
import './index.css'
import { initializeEmbeddedSession } from '@/lib/embed'
import { getLanguage, setLanguage, t } from '@/i18n'
import { useUIStore } from '@/stores/ui'

// The persisted UI store owns the language. Reading it here forces its
// localStorage rehydration (whose `onRehydrateStorage` writes the value into the
// i18n module) to finish before the first render, so the first paint already has
// the right dictionary and `<html lang>` - no flash of the wrong language, and
// no wrong-language `Intl` formatting on the first render. The guard keeps this
// a no-op when rehydration already wrote the same value.
const bootLanguage = useUIStore.getState().language
if (getLanguage() !== bootLanguage) setLanguage(bootLanguage)

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
