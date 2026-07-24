import { useEffect, useState } from "react"
import api, { errMessage } from "@/lib/api"
import { Button } from "@/components/ui/button"
import { Label } from "@/components/ui/label"
import { Select } from "@/components/ui/select"
import { toast } from "@/lib/toast"
import { useT } from "@/lib/i18n"

// Настройка политики принуждения MFA (фича MFA enforce-by-policy, миграция 054). it_admin
// выбирает, для кого 2FA обязательна: выкл (по желанию) | только администраторы | все. Сервер
// затем принуждает попавших под политику юзеров без MFA включить её (гейт + баннер).
type MfaPolicyValue = "" | "it_admin" | "all"

const M = {
  title: { ru: "Политика двухфакторной аутентификации", en: "Two-factor authentication policy" },
  sub: {
    ru: "Кого обязать включить 2FA. Попавшие под политику без включённой 2FA будут ограничены в действиях, пока не включат её.",
    en: "Who must enable 2FA. Users covered by the policy without 2FA will be restricted until they enable it.",
  },
  label: { ru: "Требовать 2FA", en: "Require 2FA" },
  optOff: { ru: "Выключено (по желанию)", en: "Off (optional)" },
  optAdmin: { ru: "Только администраторы", en: "Administrators only" },
  optAll: { ru: "Все пользователи", en: "Everyone" },
  save: { ru: "Сохранить", en: "Save" },
  saving: { ru: "Сохранение...", en: "Saving..." },
  saved: { ru: "Политика MFA обновлена", en: "MFA policy updated" },
  selfHint: {
    ru: "Убедитесь, что у вас самих включена 2FA — иначе после сохранения вы будете перенаправлены на её включение.",
    en: "Make sure your own 2FA is enabled — otherwise you will be redirected to enable it after saving.",
  },
  loadErr: { ru: "Не удалось загрузить политику MFA", en: "Failed to load MFA policy" },
  genericErr: { ru: "Ошибка", en: "Error" },
}

export default function MfaPolicySettings() {
  const t = useT()
  const [loading, setLoading] = useState(true)
  const [saved, setSaved] = useState<MfaPolicyValue>("")
  const [value, setValue] = useState<MfaPolicyValue>("")
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    api.get<{ mfa_required_role: MfaPolicyValue }>("/settings/mfa-policy")
      .then((r) => {
        const v = r.data.mfa_required_role ?? ""
        setSaved(v)
        setValue(v)
      })
      .catch((e) => toast({ title: t(M.loadErr), description: errMessage(e), variant: "destructive" }))
      .finally(() => setLoading(false))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  async function save() {
    setBusy(true)
    try {
      await api.put("/settings/mfa-policy", { mfa_required_role: value })
      setSaved(value)
      toast({ title: t(M.saved), variant: "success" })
    } catch (e) {
      toast({ title: t(M.genericErr), description: errMessage(e), variant: "destructive" })
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="glass px-5 py-[18px] flex flex-col gap-4 max-w-lg">
      <div>
        <h2 className="text-[15px] font-semibold text-foreground">{t(M.title)}</h2>
        <p className="text-xs text-muted-foreground">{t(M.sub)}</p>
      </div>
      <div className="space-y-1.5">
        <Label className="text-soft">{t(M.label)}</Label>
        <Select
          value={value}
          onChange={(v) => setValue(v as MfaPolicyValue)}
          className="max-w-[320px]"
          options={[
            { value: "", label: t(M.optOff) },
            { value: "it_admin", label: t(M.optAdmin) },
            { value: "all", label: t(M.optAll) },
          ]}
        />
      </div>
      {value !== "" && value !== saved && (
        <p className="text-xs text-amber-700 dark:text-amber-400">{t(M.selfHint)}</p>
      )}
      <Button
        type="button"
        className="self-start"
        disabled={busy || loading || value === saved}
        onClick={save}
      >
        {busy ? t(M.saving) : t(M.save)}
      </Button>
    </div>
  )
}
