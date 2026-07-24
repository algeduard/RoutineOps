import { useState, useEffect, FormEvent } from "react"
import api from "@/lib/api"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Select } from "@/components/ui/select"
import { Badge } from "@/components/ui/badge"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { UserPlus } from "lucide-react"
import { toast } from "@/lib/toast"
import { useT, type Msg } from "@/lib/i18n"
import { useMe } from "@/lib/useMe"
import MfaPolicySettings from "@/components/MfaPolicySettings"

interface User {
  id: string
  name: string
  email: string
  role: string
  created_at: string
}

const roleLabels: Record<string, Msg> = {
  it_admin: { ru: "IT-администратор", en: "IT administrator" },
  viewer: { ru: "Наблюдатель", en: "Viewer" },
}

const M = {
  title: { ru: "Пользователи", en: "Users" },
  invite: { ru: "Пригласить", en: "Invite" },
  accounts: { ru: "Учётные записи", en: "Accounts" },
  accountsSub: { ru: "Доступ к панели управления", en: "Admin console access" },
  searchEmail: { ru: "Поиск по email...", en: "Search by email..." },
  colName: { ru: "Имя", en: "Name" },
  colEmail: { ru: "Email", en: "Email" },
  colRole: { ru: "Роль", en: "Role" },
  colAdded: { ru: "Добавлен", en: "Added" },
  loading: { ru: "Загрузка...", en: "Loading..." },
  noUsers: { ru: "Пользователей нет", en: "No users" },
  nothingFound: { ru: "Ничего не найдено", en: "Nothing found" },
  inviteUser: { ru: "Пригласить пользователя", en: "Invite a user" },
  emailLabel: { ru: "Email", en: "Email" },
  roleLabel: { ru: "Роль", en: "Role" },
  inviteLinkLabel: { ru: "Ссылка-приглашение (передайте вручную)", en: "Invite link (share manually)" },
  inviteLinkHint: {
    ru: "Письмо не отправлено (SMTP выключен или недоступен). Скопируйте ссылку и передайте пользователю.",
    en: "The email was not sent (SMTP is disabled or unavailable). Copy the link and share it with the user.",
  },
  close: { ru: "Закрыть", en: "Close" },
  cancel: { ru: "Отмена", en: "Cancel" },
  sending: { ru: "Отправка...", en: "Sending..." },
  sendInvite: { ru: "Отправить приглашение", en: "Send invite" },
  loadErr: { ru: "Не удалось загрузить пользователей", en: "Failed to load users" },
  inviteSent: { ru: "Приглашение отправлено на {email}", en: "Invite sent to {email}" },
  emailNotSentCopyLink: {
    ru: "Письмо не отправлено — скопируйте ссылку-приглашение вручную",
    en: "Email not sent — copy the invite link manually",
  },
  inviteCreatedNoLink: {
    ru: "Приглашение создано, но письмо не отправлено и ссылка недоступна",
    en: "Invite created, but the email was not sent and no link is available",
  },
  inviteErr: { ru: "Не удалось отправить приглашение", en: "Failed to send invite" },
  colActions: { ru: "Действия", en: "Actions" },
  resetMfa: { ru: "Сбросить 2FA", en: "Reset 2FA" },
  resetMfaTitle: { ru: "Сбросить двухфакторную аутентификацию", en: "Reset two-factor authentication" },
  resetMfaBody: {
    ru: "Снять MFA у {email}? Пользователь сможет войти по паролю и заново её настроить. Действие записывается в аудит.",
    en: "Remove MFA for {email}? The user will sign in with their password and set it up again. This is recorded in the audit log.",
  },
  resetMfaOk: { ru: "MFA сброшена", en: "MFA reset" },
  resetMfaErr: { ru: "Не удалось сбросить MFA", en: "Failed to reset MFA" },
  confirm: { ru: "Сбросить", en: "Reset" },
}

export default function Users() {
  const t = useT()
  const { me } = useMe()
  const isAdmin = me?.role === "it_admin"
  const colCount = isAdmin ? 5 : 4
  const [users, setUsers] = useState<User[]>([])
  const [loading, setLoading] = useState(true)
  const [query, setQuery] = useState("")
  const [inviteOpen, setInviteOpen] = useState(false)
  const [inviteEmail, setInviteEmail] = useState("")
  const [inviteRole, setInviteRole] = useState("it_admin")
  const [inviteLoading, setInviteLoading] = useState(false)
  // Пользователь, которому сбрасываем MFA (открывает диалог подтверждения).
  const [resetUser, setResetUser] = useState<User | null>(null)
  const [resetLoading, setResetLoading] = useState(false)
  // Ссылка-приглашение, если письмо не ушло (SMTP выключен или отправка не удалась):
  // бэкенд возвращает invite_url, чтобы оператор передал её вручную.
  const [inviteLink, setInviteLink] = useState<string | null>(null)

  useEffect(() => {
    api.get<User[]>("/users")
      .then((r) => setUsers(r.data))
      .catch(() => toast({ title: t(M.loadErr), variant: "destructive" }))
      .finally(() => setLoading(false))
  }, [])

  async function handleResetMfa() {
    if (!resetUser) return
    setResetLoading(true)
    try {
      await api.post(`/users/${resetUser.id}/mfa/reset`)
      toast({ title: t(M.resetMfaOk), variant: "success" })
      setResetUser(null)
    } catch {
      toast({ title: t(M.resetMfaErr), variant: "destructive" })
    } finally {
      setResetLoading(false)
    }
  }

  async function handleInvite(e: FormEvent) {
    e.preventDefault()
    setInviteLoading(true)
    setInviteLink(null)
    try {
      const r = await api.post<{ email_sent?: string; invite_url?: string }>(
        "/users/invite", { email: inviteEmail, role: inviteRole })
      if (r.data.email_sent === "true") {
        toast({ title: t(M.inviteSent, { email: inviteEmail }), variant: "success" })
        setInviteOpen(false)
        setInviteEmail("")
        return
      }
      // Письмо не ушло (SMTP выключен/сбой) — показываем ссылку для ручной передачи,
      // диалог НЕ закрываем, иначе ссылка потеряется.
      if (r.data.invite_url) {
        setInviteLink(r.data.invite_url)
        toast({ title: t(M.emailNotSentCopyLink), variant: "destructive" })
      } else {
        toast({ title: t(M.inviteCreatedNoLink), variant: "destructive" })
      }
    } catch {
      toast({ title: t(M.inviteErr), variant: "destructive" })
    } finally {
      setInviteLoading(false)
    }
  }

  return (
    <div className="flex flex-col gap-5">
      <div className="flex items-center justify-between gap-4">
        <h1 className="text-xl font-semibold text-foreground">{t(M.title)}</h1>
        <Button onClick={() => setInviteOpen(true)}>
          <UserPlus className="h-4 w-4 mr-2" strokeWidth={2} />
          {t(M.invite)}
        </Button>
      </div>

      <div className="glass">
        <div className="flex flex-wrap items-center justify-between gap-3 px-5 pt-4 pb-3">
          <div>
            <h2 className="text-[15px] font-semibold text-foreground">{t(M.accounts)}</h2>
            <p className="text-xs text-muted-foreground">{t(M.accountsSub)}</p>
          </div>
          <Input
            placeholder={t(M.searchEmail)}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            className="max-w-[240px]"
          />
        </div>

        {/* Строки таблицы разделяются верхней границей (как ленты на «Обзоре»),
            поэтому border-b примитива гасится, а border-t проставляется явно. */}
        <Table>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead className="px-5 text-xs font-medium text-muted-foreground">{t(M.colName)}</TableHead>
              <TableHead className="px-5 text-xs font-medium text-muted-foreground">{t(M.colEmail)}</TableHead>
              <TableHead className="px-5 text-xs font-medium text-muted-foreground">{t(M.colRole)}</TableHead>
              <TableHead className="px-5 text-xs font-medium text-muted-foreground">{t(M.colAdded)}</TableHead>
              {isAdmin && <TableHead className="px-5 text-xs font-medium text-muted-foreground text-right">{t(M.colActions)}</TableHead>}
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading ? (
              <TableRow className="hover:bg-transparent"><TableCell colSpan={colCount} className="text-center text-xs text-muted-foreground py-8">{t(M.loading)}</TableCell></TableRow>
            ) : users.length === 0 ? (
              <TableRow className="hover:bg-transparent"><TableCell colSpan={colCount} className="text-center text-xs text-muted-foreground py-8">{t(M.noUsers)}</TableCell></TableRow>
            ) : (() => {
              const q = query.trim().toLowerCase()
              const filtered = q ? users.filter((u) => u.email.toLowerCase().includes(q)) : users
              if (filtered.length === 0) {
                return <TableRow className="hover:bg-transparent"><TableCell colSpan={colCount} className="text-center text-xs text-muted-foreground py-8">{t(M.nothingFound)}</TableCell></TableRow>
              }
              return filtered.map((u) => (
              <TableRow key={u.id} className="hover:bg-transparent">
                <TableCell className="px-5 py-3 text-sm font-medium text-foreground">{u.name}</TableCell>
                <TableCell className="px-5 py-3 text-[13px] text-soft">{u.email}</TableCell>
                <TableCell className="px-5 py-3">
                  <Badge variant={u.role === "it_admin" ? "default" : "outline"}>
                    {roleLabels[u.role] ? t(roleLabels[u.role]) : u.role}
                  </Badge>
                </TableCell>
                <TableCell className="px-5 py-3 text-xs text-muted-foreground tabular-nums">
                  {new Date(u.created_at).toLocaleDateString("ru-RU")}
                </TableCell>
                {isAdmin && (
                  <TableCell className="px-5 py-3 text-right">
                    <Button variant="outline" size="sm" onClick={() => setResetUser(u)}>{t(M.resetMfa)}</Button>
                  </TableCell>
                )}
              </TableRow>
              ))
            })()}
          </TableBody>
        </Table>
      </div>

      {/* Политика принуждения MFA (миграция 054): страница Users — admin-only, поэтому
          настройка живёт здесь рядом с управлением доступом. */}
      <MfaPolicySettings />

      <Dialog open={inviteOpen} onOpenChange={(o) => { setInviteOpen(o); if (!o) { setInviteLink(null); setInviteEmail("") } }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t(M.inviteUser)}</DialogTitle>
          </DialogHeader>
          <form onSubmit={handleInvite} className="space-y-4 pt-2">
            <div className="space-y-1.5">
              <Label className="text-soft">{t(M.emailLabel)}</Label>
              <Input
                type="email"
                value={inviteEmail}
                onChange={(e) => setInviteEmail(e.target.value)}
                placeholder="colleague@company.com"
                required
                autoFocus
              />
            </div>
            <div className="space-y-1.5">
              <Label className="text-soft">{t(M.roleLabel)}</Label>
              <Select
                value={inviteRole}
                onChange={setInviteRole}
                options={[
                  { value: "it_admin", label: t(roleLabels.it_admin) },
                  { value: "viewer", label: t(roleLabels.viewer) },
                ]}
              />
            </div>
            {inviteLink && (
              <div className="space-y-1.5">
                <Label className="text-soft">{t(M.inviteLinkLabel)}</Label>
                <Input readOnly value={inviteLink} onFocus={(e) => e.currentTarget.select()} />
                <p className="text-xs text-muted-foreground">
                  {t(M.inviteLinkHint)}
                </p>
              </div>
            )}
            <div className="flex justify-end gap-2">
              <Button type="button" variant="outline" onClick={() => setInviteOpen(false)}>
                {inviteLink ? t(M.close) : t(M.cancel)}
              </Button>
              <Button type="submit" disabled={inviteLoading}>
                {inviteLoading ? t(M.sending) : t(M.sendInvite)}
              </Button>
            </div>
          </form>
        </DialogContent>
      </Dialog>

      <Dialog open={resetUser !== null} onOpenChange={(o) => { if (!o) setResetUser(null) }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t(M.resetMfaTitle)}</DialogTitle>
          </DialogHeader>
          <p className="text-sm text-muted-foreground pt-1">
            {t(M.resetMfaBody, { email: resetUser?.email ?? "" })}
          </p>
          <div className="flex justify-end gap-2 pt-2">
            <Button type="button" variant="outline" onClick={() => setResetUser(null)}>{t(M.cancel)}</Button>
            <Button type="button" variant="destructive" disabled={resetLoading} onClick={handleResetMfa}>
              {resetLoading ? t(M.sending) : t(M.confirm)}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  )
}
