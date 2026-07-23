import { useState, useEffect, FormEvent } from "react"
import api, { Tenant, Device, errStatus } from "@/lib/api"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Badge } from "@/components/ui/badge"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Building2, Plus } from "lucide-react"
import { toast } from "@/lib/toast"
import { useT } from "@/lib/i18n"

// User — минимум для назначения (список /users отдаёт больше полей, но нам нужны эти).
interface AssignUser {
  id: string
  name: string
  email: string
}

const M = {
  title: { ru: "Тенанты", en: "Tenants" },
  intro: {
    ru: "Арендаторы (организации/подразделения) и привязка устройств и пользователей к ним. Существующие сущности уже находятся в тенанте «Default». Это MVP: модель и назначение; полная изоляция данных между тенантами — в разработке.",
    en: "Tenants (organizations/departments) and assignment of devices and users to them. Existing entities already live in the “Default” tenant. This is an MVP: model and assignment; full data isolation between tenants is in progress.",
  },
  create: { ru: "Создать тенант", en: "Create tenant" },
  unavailableTitle: { ru: "Мультитенантность недоступна в этой редакции", en: "Multitenancy is not available in this edition" },
  unavailableBody: {
    ru: "Тенанты — функция редакции Enterprise. Нужна активная лицензия, покрывающая эту фичу.",
    en: "Tenants are an Enterprise-edition feature. They require an active license covering it.",
  },
  loading: { ru: "Загрузка...", en: "Loading..." },
  loadErr: { ru: "Не удалось загрузить тенанты", en: "Failed to load tenants" },
  colName: { ru: "Название", en: "Name" },
  colSlug: { ru: "Slug", en: "Slug" },
  colDevices: { ru: "Устройств", en: "Devices" },
  colUsers: { ru: "Пользователей", en: "Users" },
  colCreated: { ru: "Создан", en: "Created" },
  colActions: { ru: "Действия", en: "Actions" },
  noTenants: { ru: "Тенантов нет", en: "No tenants" },
  defaultBadge: { ru: "По умолчанию", en: "Default" },
  assign: { ru: "Назначить", en: "Assign" },
  rename: { ru: "Переименовать", en: "Rename" },
  delete: { ru: "Удалить", en: "Delete" },
  cancel: { ru: "Отмена", en: "Cancel" },
  save: { ru: "Сохранить", en: "Save" },
  saving: { ru: "Сохранение...", en: "Saving..." },
  // Create
  createTitle: { ru: "Создать тенант", en: "Create a tenant" },
  nameLabel: { ru: "Название", en: "Name" },
  slugLabel: { ru: "Slug (латиница, цифры, дефис)", en: "Slug (a-z, 0-9, dash)" },
  slugHint: {
    ru: "Машинный идентификатор: только строчная латиница, цифры и дефис.",
    en: "Machine identifier: lowercase letters, digits and dashes only.",
  },
  created: { ru: "Тенант создан", en: "Tenant created" },
  // Rename
  renameTitle: { ru: "Переименовать тенант", en: "Rename tenant" },
  renamed: { ru: "Тенант переименован", en: "Tenant renamed" },
  // Delete
  deleteTitle: { ru: "Удалить тенант", en: "Delete tenant" },
  deleteBody: {
    ru: "Удалить тенант «{name}»? Действие необратимо. Удалить можно только пустой тенант.",
    en: "Delete tenant “{name}”? This cannot be undone. Only an empty tenant can be deleted.",
  },
  deleted: { ru: "Тенант удалён", en: "Tenant deleted" },
  // Assign
  assignTitle: { ru: "Назначить в «{name}»", en: "Assign to “{name}”" },
  assignDevices: { ru: "Устройства", en: "Devices" },
  assignUsers: { ru: "Пользователи", en: "Users" },
  searchPlaceholder: { ru: "Поиск...", en: "Search..." },
  assignSubmit: { ru: "Назначить выбранные", en: "Assign selected" },
  assignNothing: { ru: "Выберите устройства или пользователей", en: "Select devices or users" },
  assignDone: { ru: "Назначено устройств: {d}, пользователей: {u}", en: "Assigned {d} devices and {u} users" },
  assignEmptyDevices: { ru: "Устройств нет", en: "No devices" },
  assignEmptyUsers: { ru: "Пользователей нет", en: "No users" },
}

export default function Tenants() {
  const t = useT()
  const [tenants, setTenants] = useState<Tenant[]>([])
  const [loading, setLoading] = useState(true)
  const [unavailable, setUnavailable] = useState(false)

  const [createOpen, setCreateOpen] = useState(false)
  const [renameTenant, setRenameTenant] = useState<Tenant | null>(null)
  const [deleteTenant, setDeleteTenant] = useState<Tenant | null>(null)
  const [assignTenant, setAssignTenant] = useState<Tenant | null>(null)

  async function load() {
    try {
      const r = await api.get<Tenant[]>("/tenants")
      setTenants(r.data ?? [])
    } catch (e) {
      if (errStatus(e) === 404 || errStatus(e) === 402) setUnavailable(true)
      else toast({ title: t(M.loadErr), variant: "destructive" })
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    load()
  }, [])

  if (loading) return <p className="text-muted-foreground text-sm">{t(M.loading)}</p>

  if (unavailable) {
    return (
      <div className="flex flex-col gap-5 max-w-2xl">
        <h1 className="text-xl font-semibold text-foreground">{t(M.title)}</h1>
        <div className="glass px-5 py-[18px] space-y-2">
          <div className="flex items-center gap-2">
            <Badge variant="secondary">Free</Badge>
            <span className="text-[15px] font-semibold text-foreground">{t(M.unavailableTitle)}</span>
          </div>
          <p className="text-sm text-muted-foreground">{t(M.unavailableBody)}</p>
        </div>
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-5">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-xl font-semibold text-foreground">{t(M.title)}</h1>
          <p className="text-sm text-muted-foreground mt-1 max-w-2xl">{t(M.intro)}</p>
        </div>
        <Button onClick={() => setCreateOpen(true)} className="flex-shrink-0">
          <Plus className="h-4 w-4 mr-2" strokeWidth={2} />
          {t(M.create)}
        </Button>
      </div>

      <div className="glass">
        <Table>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead className="px-5 text-xs font-medium text-muted-foreground">{t(M.colName)}</TableHead>
              <TableHead className="px-5 text-xs font-medium text-muted-foreground">{t(M.colSlug)}</TableHead>
              <TableHead className="px-5 text-xs font-medium text-muted-foreground text-right">{t(M.colDevices)}</TableHead>
              <TableHead className="px-5 text-xs font-medium text-muted-foreground text-right">{t(M.colUsers)}</TableHead>
              <TableHead className="px-5 text-xs font-medium text-muted-foreground">{t(M.colCreated)}</TableHead>
              <TableHead className="px-5 text-xs font-medium text-muted-foreground text-right">{t(M.colActions)}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {tenants.length === 0 ? (
              <TableRow className="hover:bg-transparent"><TableCell colSpan={6} className="text-center text-xs text-muted-foreground py-8">{t(M.noTenants)}</TableCell></TableRow>
            ) : (
              tenants.map((tn) => (
                <TableRow key={tn.id} className="hover:bg-transparent">
                  <TableCell className="px-5 py-3 text-sm font-medium text-foreground">
                    <span className="inline-flex items-center gap-2">
                      <Building2 className="h-4 w-4 text-muted-foreground" />
                      {tn.name}
                      {tn.is_default && <Badge variant="outline">{t(M.defaultBadge)}</Badge>}
                    </span>
                  </TableCell>
                  <TableCell className="px-5 py-3 text-[13px] text-soft font-mono">{tn.slug}</TableCell>
                  <TableCell className="px-5 py-3 text-sm text-foreground text-right tabular-nums">{tn.device_count}</TableCell>
                  <TableCell className="px-5 py-3 text-sm text-foreground text-right tabular-nums">{tn.user_count}</TableCell>
                  <TableCell className="px-5 py-3 text-xs text-muted-foreground tabular-nums">
                    {new Date(tn.created_at).toLocaleDateString("ru-RU")}
                  </TableCell>
                  <TableCell className="px-5 py-3 text-right space-x-2 whitespace-nowrap">
                    <Button variant="outline" size="sm" onClick={() => setAssignTenant(tn)}>{t(M.assign)}</Button>
                    <Button variant="outline" size="sm" onClick={() => setRenameTenant(tn)}>{t(M.rename)}</Button>
                    {!tn.is_default && (
                      <Button variant="destructive" size="sm" onClick={() => setDeleteTenant(tn)}>{t(M.delete)}</Button>
                    )}
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </div>

      {createOpen && <CreateDialog onClose={() => setCreateOpen(false)} onDone={() => { setCreateOpen(false); load() }} />}
      {renameTenant && <RenameDialog tenant={renameTenant} onClose={() => setRenameTenant(null)} onDone={() => { setRenameTenant(null); load() }} />}
      {deleteTenant && <DeleteDialog tenant={deleteTenant} onClose={() => setDeleteTenant(null)} onDone={() => { setDeleteTenant(null); load() }} />}
      {assignTenant && <AssignDialog tenant={assignTenant} onClose={() => setAssignTenant(null)} onDone={() => { setAssignTenant(null); load() }} />}
    </div>
  )
}

// slugify выводит машинный slug из названия (правится вручную в форме).
function slugify(s: string): string {
  return s.toLowerCase().trim().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 63)
}

function CreateDialog({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const t = useT()
  const [name, setName] = useState("")
  const [slug, setSlug] = useState("")
  const [slugEdited, setSlugEdited] = useState(false)
  const [saving, setSaving] = useState(false)

  const effectiveSlug = slugEdited ? slug : slugify(name)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setSaving(true)
    try {
      await api.post("/tenants", { name: name.trim(), slug: effectiveSlug })
      toast({ title: t(M.created), variant: "success" })
      onDone()
    } catch {
      // авто-тост интерцептора (409 конфликт / 400 невалидный slug)
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent>
        <DialogHeader><DialogTitle>{t(M.createTitle)}</DialogTitle></DialogHeader>
        <form onSubmit={submit} className="space-y-4 pt-2">
          <div className="space-y-1.5">
            <Label className="text-soft">{t(M.nameLabel)}</Label>
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="Acme Corp" required autoFocus />
          </div>
          <div className="space-y-1.5">
            <Label className="text-soft">{t(M.slugLabel)}</Label>
            <Input
              value={effectiveSlug}
              onChange={(e) => { setSlugEdited(true); setSlug(e.target.value) }}
              placeholder="acme-corp"
              required
            />
            <p className="text-xs text-muted-foreground">{t(M.slugHint)}</p>
          </div>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={onClose}>{t(M.cancel)}</Button>
            <Button type="submit" disabled={saving || !name.trim() || !effectiveSlug}>{saving ? t(M.saving) : t(M.save)}</Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function RenameDialog({ tenant, onClose, onDone }: { tenant: Tenant; onClose: () => void; onDone: () => void }) {
  const t = useT()
  const [name, setName] = useState(tenant.name)
  const [saving, setSaving] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setSaving(true)
    try {
      await api.patch(`/tenants/${tenant.id}`, { name: name.trim() })
      toast({ title: t(M.renamed), variant: "success" })
      onDone()
    } catch {
      // авто-тост интерцептора
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent>
        <DialogHeader><DialogTitle>{t(M.renameTitle)}</DialogTitle></DialogHeader>
        <form onSubmit={submit} className="space-y-4 pt-2">
          <div className="space-y-1.5">
            <Label className="text-soft">{t(M.nameLabel)}</Label>
            <Input value={name} onChange={(e) => setName(e.target.value)} required autoFocus />
          </div>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={onClose}>{t(M.cancel)}</Button>
            <Button type="submit" disabled={saving || !name.trim()}>{saving ? t(M.saving) : t(M.save)}</Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function DeleteDialog({ tenant, onClose, onDone }: { tenant: Tenant; onClose: () => void; onDone: () => void }) {
  const t = useT()
  const [saving, setSaving] = useState(false)

  async function submit() {
    setSaving(true)
    try {
      await api.delete(`/tenants/${tenant.id}`)
      toast({ title: t(M.deleted), variant: "success" })
      onDone()
    } catch {
      // авто-тост интерцептора (409: default / непустой)
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent>
        <DialogHeader><DialogTitle>{t(M.deleteTitle)}</DialogTitle></DialogHeader>
        <p className="text-sm text-muted-foreground pt-1">{t(M.deleteBody, { name: tenant.name })}</p>
        <div className="flex justify-end gap-2 pt-2">
          <Button type="button" variant="outline" onClick={onClose}>{t(M.cancel)}</Button>
          <Button type="button" variant="destructive" disabled={saving} onClick={submit}>{saving ? t(M.saving) : t(M.delete)}</Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}

function AssignDialog({ tenant, onClose, onDone }: { tenant: Tenant; onClose: () => void; onDone: () => void }) {
  const t = useT()
  const [devices, setDevices] = useState<Device[]>([])
  const [users, setUsers] = useState<AssignUser[]>([])
  const [selDevices, setSelDevices] = useState<Set<string>>(new Set())
  const [selUsers, setSelUsers] = useState<Set<string>>(new Set())
  const [dQuery, setDQuery] = useState("")
  const [uQuery, setUQuery] = useState("")
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    api.get<Device[]>("/devices").then((r) => setDevices(r.data ?? [])).catch(() => {})
    api.get<AssignUser[]>("/users").then((r) => setUsers(r.data ?? [])).catch(() => {})
  }, [])

  function toggle(set: Set<string>, id: string, apply: (s: Set<string>) => void) {
    const next = new Set(set)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    apply(next)
  }

  async function submit() {
    if (selDevices.size === 0 && selUsers.size === 0) {
      toast({ title: t(M.assignNothing), variant: "destructive" })
      return
    }
    setSaving(true)
    try {
      const r = await api.post<{ devices_assigned: number; users_assigned: number }>(
        `/tenants/${tenant.id}/assign`,
        { device_ids: [...selDevices], user_ids: [...selUsers] })
      toast({ title: t(M.assignDone, { d: String(r.data.devices_assigned), u: String(r.data.users_assigned) }), variant: "success" })
      onDone()
    } catch {
      // авто-тост интерцептора
    } finally {
      setSaving(false)
    }
  }

  const dq = dQuery.trim().toLowerCase()
  const filteredDevices = dq ? devices.filter((d) => d.hostname.toLowerCase().includes(dq)) : devices
  const uq = uQuery.trim().toLowerCase()
  const filteredUsers = uq ? users.filter((u) => u.email.toLowerCase().includes(uq) || u.name.toLowerCase().includes(uq)) : users

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent>
        <DialogHeader><DialogTitle>{t(M.assignTitle, { name: tenant.name })}</DialogTitle></DialogHeader>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 pt-2">
          <div className="space-y-1.5">
            <Label className="text-soft">{t(M.assignDevices)}</Label>
            <Input value={dQuery} onChange={(e) => setDQuery(e.target.value)} placeholder={t(M.searchPlaceholder)} />
            <div className="max-h-56 overflow-y-auto rounded-md border border-border divide-y divide-border">
              {filteredDevices.length === 0 ? (
                <p className="text-xs text-muted-foreground px-3 py-4 text-center">{t(M.assignEmptyDevices)}</p>
              ) : filteredDevices.map((d) => (
                <label key={d.id} className="flex items-center gap-2 px-3 py-2 text-sm cursor-pointer">
                  <input type="checkbox" checked={selDevices.has(d.id)} onChange={() => toggle(selDevices, d.id, setSelDevices)} />
                  <span className="truncate">{d.hostname}</span>
                </label>
              ))}
            </div>
          </div>
          <div className="space-y-1.5">
            <Label className="text-soft">{t(M.assignUsers)}</Label>
            <Input value={uQuery} onChange={(e) => setUQuery(e.target.value)} placeholder={t(M.searchPlaceholder)} />
            <div className="max-h-56 overflow-y-auto rounded-md border border-border divide-y divide-border">
              {filteredUsers.length === 0 ? (
                <p className="text-xs text-muted-foreground px-3 py-4 text-center">{t(M.assignEmptyUsers)}</p>
              ) : filteredUsers.map((u) => (
                <label key={u.id} className="flex items-center gap-2 px-3 py-2 text-sm cursor-pointer">
                  <input type="checkbox" checked={selUsers.has(u.id)} onChange={() => toggle(selUsers, u.id, setSelUsers)} />
                  <span className="truncate">{u.email}</span>
                </label>
              ))}
            </div>
          </div>
        </div>
        <div className="flex justify-end gap-2 pt-2">
          <Button type="button" variant="outline" onClick={onClose}>{t(M.cancel)}</Button>
          <Button type="button" disabled={saving || (selDevices.size === 0 && selUsers.size === 0)} onClick={submit}>
            {saving ? t(M.saving) : t(M.assignSubmit)}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}
