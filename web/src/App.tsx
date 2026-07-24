import { Routes, Route, Navigate } from "react-router-dom"
import { isAuthenticated } from "@/lib/auth"
import { useMe } from "@/lib/useMe"
import Layout from "@/components/Layout"
import Toaster from "@/components/Toaster"
import Dashboard from "@/pages/Dashboard"
import Login from "@/pages/Login"
import ForgotPassword from "@/pages/ForgotPassword"
import ResetPassword from "@/pages/ResetPassword"
import AcceptInvite from "@/pages/AcceptInvite"
import Devices from "@/pages/Devices"
import DeviceDetail from "@/pages/DeviceDetail"
import RemoteDesktop from "@/pages/RemoteDesktop"
import Alerts from "@/pages/Alerts"
import AdminAccess from "@/pages/AdminAccess"
import HelpRequests from "@/pages/HelpRequests"
import EnrollmentQueue from "@/pages/EnrollmentQueue"
import Migration from "@/pages/Migration"
import Policies from "@/pages/Policies"
import PolicyDetail from "@/pages/PolicyDetail"
import Scripts from "@/pages/Scripts"
import ScriptPolicies from "@/pages/ScriptPolicies"
import Groups from "@/pages/Groups"
import AuditLog from "@/pages/AuditLog"
import Users from "@/pages/Users"
import License from "@/pages/License"
import SiemExport from "@/pages/SiemExport"
import Compliance from "@/pages/Compliance"
import Cve from "@/pages/Cve"
import Tenants from "@/pages/Tenants"
import Scim from "@/pages/Scim"
import AlertRouting from "@/pages/AlertRouting"
import Reports from "@/pages/Reports"
import Profile from "@/pages/Profile"

function PrivateRoute({ children }: { children: React.ReactNode }) {
  return isAuthenticated() ? <>{children}</> : <Navigate to="/login" replace />
}

// AdminRoute гейтит admin-only страницы: viewer редиректится на «Обзор».
// Пока роль грузится (/me) — ничего не рендерим, чтобы не мигнуть редиректом у админа.
// Бэкенд всё равно 403'ит мутации — это UX-слой поверх nav-гейтинга.
function AdminRoute({ children }: { children: React.ReactNode }) {
  const { isAdmin, loading } = useMe()
  if (loading) return null
  return isAdmin ? <>{children}</> : <Navigate to="/" replace />
}

export default function App() {
  return (
    <>
      <Toaster />
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route path="/forgot-password" element={<ForgotPassword />} />
        <Route path="/reset-password" element={<ResetPassword />} />
        <Route path="/accept-invite" element={<AcceptInvite />} />
        <Route
          path="/"
          element={
            <PrivateRoute>
              <Layout />
            </PrivateRoute>
          }
        >
          <Route index element={<Dashboard />} />
          <Route path="dashboard" element={<Navigate to="/" replace />} />
          <Route path="devices" element={<Devices />} />
          <Route path="devices/:id" element={<DeviceDetail />} />
          <Route path="devices/:id/remote-desktop" element={<AdminRoute><RemoteDesktop /></AdminRoute>} />
          <Route path="alerts" element={<Alerts />} />
          <Route path="help-requests" element={<HelpRequests />} />
          <Route path="enrollment" element={<AdminRoute><EnrollmentQueue /></AdminRoute>} />
          <Route path="migration" element={<AdminRoute><Migration /></AdminRoute>} />
          <Route path="admin-access" element={<AdminRoute><AdminAccess /></AdminRoute>} />
          <Route path="policies" element={<AdminRoute><Policies /></AdminRoute>} />
          <Route path="policies/:id" element={<AdminRoute><PolicyDetail /></AdminRoute>} />
          <Route path="scripts" element={<AdminRoute><Scripts /></AdminRoute>} />
          <Route path="script-policies" element={<AdminRoute><ScriptPolicies /></AdminRoute>} />
          <Route path="groups" element={<AdminRoute><Groups /></AdminRoute>} />
          <Route path="audit-log" element={<AuditLog />} />
          <Route path="users" element={<AdminRoute><Users /></AdminRoute>} />
          <Route path="license" element={<AdminRoute><License /></AdminRoute>} />
          <Route path="siem" element={<AdminRoute><SiemExport /></AdminRoute>} />
          <Route path="compliance" element={<AdminRoute><Compliance /></AdminRoute>} />
          <Route path="cve" element={<AdminRoute><Cve /></AdminRoute>} />
          <Route path="tenants" element={<AdminRoute><Tenants /></AdminRoute>} />
          <Route path="scim" element={<AdminRoute><Scim /></AdminRoute>} />
          <Route path="alert-routing" element={<AdminRoute><AlertRouting /></AdminRoute>} />
          <Route path="reports" element={<AdminRoute><Reports /></AdminRoute>} />
          <Route path="profile" element={<Profile />} />
        </Route>
      </Routes>
    </>
  )
}
