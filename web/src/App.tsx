import { Navigate, Route, Routes } from 'react-router'
import { TooltipProvider } from '@/components/ui/tooltip'
import { Toaster } from '@/components/ui/sonner'
import { AppShell } from '@/components/app-shell'
import { RequireAuth } from '@/components/require-auth'
import { HashRedirect } from '@/components/hash-redirect'
import { LoginPage } from '@/routes/login'
import { ProjectsPage } from '@/routes/projects'
import { ProjectDetailPage } from '@/routes/project-detail'
import { DatabaseDetailPage } from '@/routes/database-detail'
import { ExplorerPage } from '@/routes/explorer'
import { TokensPage } from '@/routes/tokens'
import { DevicesPage } from '@/routes/devices'
import { SettingsPage } from '@/routes/settings'
import { NotFoundPage } from '@/routes/not-found'
import { useTheme } from '@/lib/theme'

export function App() {
  const { resolved } = useTheme()

  return (
    <TooltipProvider delayDuration={300}>
      <HashRedirect />
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route
          element={
            <RequireAuth>
              <AppShell />
            </RequireAuth>
          }
        >
          <Route path="/" element={<Navigate to="/projects" replace />} />
          <Route path="/projects" element={<ProjectsPage />} />
          <Route path="/projects/:name" element={<ProjectDetailPage />} />
          <Route path="/projects/:name/databases/:env" element={<DatabaseDetailPage />} />
          <Route path="/projects/:name/databases/:env/explore" element={<ExplorerPage />} />
          <Route path="/tokens" element={<TokensPage />} />
          {/* The device verification URI the CLI prints. Same screen, with the
              code prefilled from ?code=. */}
          <Route path="/devices" element={<DevicesPage />} />
          <Route path="/device" element={<DevicesPage />} />
          <Route path="/settings" element={<SettingsPage />} />
          <Route path="*" element={<NotFoundPage />} />
        </Route>
      </Routes>
      <Toaster theme={resolved} position="bottom-right" richColors closeButton />
    </TooltipProvider>
  )
}
