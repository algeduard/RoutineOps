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

interface User {
  id: string
  name: string
  email: string
  role: string
  created_at: string
}

const roleLabels: Record<string, string> = {
  it_admin: "IT-администратор",
  viewer: "Наблюдатель",
}

export default function Users() {
  const [users, setUsers] = useState<User[]>([])
  const [loading, setLoading] = useState(true)
  const [query, setQuery] = useState("")
  const [inviteOpen, setInviteOpen] = useState(false)
  const [inviteEmail, setInviteEmail] = useState("")
  const [inviteRole, setInviteRole] = useState("it_admin")
  const [inviteLoading, setInviteLoading] = useState(false)
  // Ссылка-приглашение, если письмо не ушло (SMTP выключен или отправка не удалась):
  // бэкенд возвращает invite_url, чтобы оператор передал её вручную.
  const [inviteLink, setInviteLink] = useState<string | null>(null)

  useEffect(() => {
    api.get<User[]>("/users")
      .then((r) => setUsers(r.data))
      .catch(() => toast({ title: "Не удалось загрузить пользователей", variant: "destructive" }))
      .finally(() => setLoading(false))
  }, [])

  async function handleInvite(e: FormEvent) {
    e.preventDefault()
    setInviteLoading(true)
    setInviteLink(null)
    try {
      const r = await api.post<{ email_sent?: string; invite_url?: string }>(
        "/users/invite", { email: inviteEmail, role: inviteRole })
      if (r.data.email_sent === "true") {
        toast({ title: `Приглашение отправлено на ${inviteEmail}`, variant: "success" })
        setInviteOpen(false)
        setInviteEmail("")
        return
      }
      // Письмо не ушло (SMTP выключен/сбой) — показываем ссылку для ручной передачи,
      // диалог НЕ закрываем, иначе ссылка потеряется.
      if (r.data.invite_url) {
        setInviteLink(r.data.invite_url)
        toast({ title: "Письмо не отправлено — скопируйте ссылку-приглашение вручную", variant: "destructive" })
      } else {
        toast({ title: "Приглашение создано, но письмо не отправлено и ссылка недоступна", variant: "destructive" })
      }
    } catch {
      toast({ title: "Не удалось отправить приглашение", variant: "destructive" })
    } finally {
      setInviteLoading(false)
    }
  }

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">Пользователи</h1>
        <Button onClick={() => setInviteOpen(true)}>
          <UserPlus className="w-4 h-4 mr-2" />
          Пригласить
        </Button>
      </div>

      <Input
        placeholder="Поиск по email..."
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        className="max-w-sm"
      />

      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Имя</TableHead>
            <TableHead>Email</TableHead>
            <TableHead>Роль</TableHead>
            <TableHead>Добавлен</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {loading ? (
            <TableRow><TableCell colSpan={4} className="text-center text-muted-foreground py-8">Загрузка...</TableCell></TableRow>
          ) : users.length === 0 ? (
            <TableRow><TableCell colSpan={4} className="text-center text-muted-foreground py-8">Пользователей нет</TableCell></TableRow>
          ) : (() => {
            const q = query.trim().toLowerCase()
            const filtered = q ? users.filter((u) => u.email.toLowerCase().includes(q)) : users
            if (filtered.length === 0) {
              return <TableRow><TableCell colSpan={4} className="text-center text-muted-foreground py-8">Ничего не найдено</TableCell></TableRow>
            }
            return filtered.map((u) => (
            <TableRow key={u.id}>
              <TableCell className="font-medium">{u.name}</TableCell>
              <TableCell className="text-muted-foreground">{u.email}</TableCell>
              <TableCell>
                <Badge variant={u.role === "it_admin" ? "default" : "secondary"}>
                  {roleLabels[u.role] ?? u.role}
                </Badge>
              </TableCell>
              <TableCell className="text-muted-foreground text-sm">
                {new Date(u.created_at).toLocaleDateString("ru-RU")}
              </TableCell>
            </TableRow>
            ))
          })()}
        </TableBody>
      </Table>

      <Dialog open={inviteOpen} onOpenChange={(o) => { setInviteOpen(o); if (!o) { setInviteLink(null); setInviteEmail("") } }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Пригласить пользователя</DialogTitle>
          </DialogHeader>
          <form onSubmit={handleInvite} className="space-y-4 pt-2">
            <div className="space-y-1.5">
              <Label>Email</Label>
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
              <Label>Роль</Label>
              <Select
                value={inviteRole}
                onChange={setInviteRole}
                options={[
                  { value: "it_admin", label: "IT-администратор" },
                  { value: "viewer", label: "Наблюдатель" },
                ]}
              />
            </div>
            {inviteLink && (
              <div className="space-y-1.5">
                <Label>Ссылка-приглашение (передайте вручную)</Label>
                <Input readOnly value={inviteLink} onFocus={(e) => e.currentTarget.select()} />
                <p className="text-xs text-muted-foreground">
                  Письмо не отправлено (SMTP выключен или недоступен). Скопируйте ссылку и передайте пользователю.
                </p>
              </div>
            )}
            <div className="flex justify-end gap-2">
              <Button type="button" variant="outline" onClick={() => setInviteOpen(false)}>
                {inviteLink ? "Закрыть" : "Отмена"}
              </Button>
              <Button type="submit" disabled={inviteLoading}>
                {inviteLoading ? "Отправка..." : "Отправить приглашение"}
              </Button>
            </div>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  )
}
