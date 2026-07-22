import { useState, FormEvent } from "react"
import api, { errMessage } from "@/lib/api"
import { useMe } from "@/lib/useMe"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Badge } from "@/components/ui/badge"
import { toast } from "@/lib/toast"
import { useT, type Msg } from "@/lib/i18n"

const roleLabels: Record<string, Msg> = {
  it_admin: { ru: "IT-администратор", en: "IT administrator" },
  viewer: { ru: "Наблюдатель", en: "Viewer" },
}

const M = {
  title: { ru: "Профиль", en: "Profile" },
  account: { ru: "Учётная запись", en: "Account" },
  accountSub: { ru: "Данные пользователя", en: "User details" },
  name: { ru: "Имя", en: "Name" },
  email: { ru: "Email", en: "Email" },
  role: { ru: "Роль", en: "Role" },
  changePassword: { ru: "Смена пароля", en: "Change password" },
  changePasswordSub: { ru: "Введите текущий и новый пароль", en: "Enter your current and new password" },
  currentPassword: { ru: "Текущий пароль", en: "Current password" },
  newPassword: { ru: "Новый пароль", en: "New password" },
  repeatPassword: { ru: "Повторите новый пароль", en: "Repeat new password" },
  saving: { ru: "Сохранение...", en: "Saving..." },
  submit: { ru: "Сменить пароль", en: "Change password" },
  mismatch: { ru: "Новые пароли не совпадают", en: "The new passwords do not match" },
  changed: { ru: "Пароль изменён", en: "Password changed" },
  changeErr: { ru: "Не удалось сменить пароль", en: "Failed to change password" },
}

export default function Profile() {
  const t = useT()
  const { me } = useMe()
  const [current, setCurrent] = useState("")
  const [next, setNext] = useState("")
  const [confirm, setConfirm] = useState("")
  const [loading, setLoading] = useState(false)

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    if (next !== confirm) {
      toast({ title: t(M.mismatch), variant: "destructive" })
      return
    }
    setLoading(true)
    try {
      await api.post("/me/password", { current_password: current, new_password: next })
      toast({ title: t(M.changed), variant: "success" })
      setCurrent("")
      setNext("")
      setConfirm("")
    } catch (e) {
      toast({ title: t(M.changeErr), description: errMessage(e), variant: "destructive" })
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="flex flex-col gap-5 max-w-lg">
      <h1 className="text-xl font-semibold text-foreground">{t(M.title)}</h1>

      <div className="glass px-5 py-[18px]">
        <h2 className="text-[15px] font-semibold text-foreground">{t(M.account)}</h2>
        <p className="text-xs text-muted-foreground mb-3.5">{t(M.accountSub)}</p>
        <div className="flex flex-col gap-2.5 text-[13px]">
          <div className="flex items-center justify-between gap-4">
            <span className="text-soft">{t(M.name)}</span>
            <span className="text-foreground truncate">{me?.name ?? "—"}</span>
          </div>
          <div className="flex items-center justify-between gap-4">
            <span className="text-soft">{t(M.email)}</span>
            <span className="text-foreground truncate">{me?.email ?? "—"}</span>
          </div>
          <div className="flex items-center justify-between gap-4">
            <span className="text-soft">{t(M.role)}</span>
            {me && <Badge variant={me.role === "it_admin" ? "default" : "secondary"}>{roleLabels[me.role] ? t(roleLabels[me.role]) : me.role}</Badge>}
          </div>
        </div>
      </div>

      <form onSubmit={handleSubmit} className="glass px-5 py-[18px] flex flex-col gap-4">
        <div>
          <h2 className="text-[15px] font-semibold text-foreground">{t(M.changePassword)}</h2>
          <p className="text-xs text-muted-foreground">{t(M.changePasswordSub)}</p>
        </div>
        <div className="space-y-1.5">
          <Label className="text-soft">{t(M.currentPassword)}</Label>
          <Input type="password" value={current} onChange={(e) => setCurrent(e.target.value)} required autoComplete="current-password" />
        </div>
        <div className="space-y-1.5">
          <Label className="text-soft">{t(M.newPassword)}</Label>
          <Input type="password" value={next} onChange={(e) => setNext(e.target.value)} required autoComplete="new-password" />
        </div>
        <div className="space-y-1.5">
          <Label className="text-soft">{t(M.repeatPassword)}</Label>
          <Input type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} required autoComplete="new-password" />
        </div>
        <Button type="submit" disabled={loading} className="self-start">{loading ? t(M.saving) : t(M.submit)}</Button>
      </form>
    </div>
  )
}
