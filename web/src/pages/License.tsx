import { useEffect, useState, FormEvent } from "react"
import api, { LicenseStatus, errStatus } from "@/lib/api"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import ConfirmDialog from "@/components/ConfirmDialog"
import { toast } from "@/lib/toast"
import { useT } from "@/lib/i18n"

const M = {
  wholeEdition: { ru: "вся редакция", en: "entire edition" },
  loadErr: { ru: "Не удалось загрузить статус лицензии", en: "Failed to load license status" },
  appliedNotSaved: { ru: "Применена, но не сохранена на диск", en: "Applied, but not saved to disk" },
  disabledNotDeleted: { ru: "Отключена, но не удалена с диска", en: "Disabled, but not deleted from disk" },
  acceptedNotInTerm: { ru: "Лицензия принята, но не в сроке", en: "License accepted, but outside its validity period" },
  acceptedNotInTermDesc: {
    ru: "Подпись верна, однако период действия ещё не начался или уже закончился — enterprise-функции не включены.",
    en: "The signature is valid, but the validity period has not started yet or has already ended — enterprise features are not enabled.",
  },
  licenseApplied: { ru: "Лицензия применена", en: "License applied" },
  licenseDeactivated: { ru: "Лицензия деактивирована", en: "License deactivated" },
  licenseAppliedDesc: { ru: "Изменения действуют сразу, без рестарта.", en: "Changes take effect immediately, without a restart." },
  licenseDeactivatedDesc: { ru: "Сервер работает в редакции Free.", en: "The server is running the Free edition." },
  loading: { ru: "Загрузка...", en: "Loading..." },
  title: { ru: "Лицензия", en: "License" },
  unavailableTitle: { ru: "Лицензирование недоступно в этой редакции", en: "Licensing is not available in this edition" },
  unavailableBody: {
    ru: "Эта сборка — open-core RoutineOps: весь операционный MDM работает без лицензии и без ограничений. Лицензионный ключ нужен только редакции Enterprise (SSO, FileVault, расширенный compliance, мульти-тенантность).",
    en: "This build is open-core RoutineOps: the entire operational MDM works without a license and without limits. A license key is required only for the Enterprise edition (SSO, FileVault, extended compliance, multi-tenancy).",
  },
  statusLoadErrTitle: { ru: "Не удалось получить статус лицензии", en: "Failed to retrieve license status" },
  statusLoadErrBody: {
    ru: "Состояние неизвестно — сервер не ответил. Это не значит, что лицензии нет.",
    en: "The state is unknown — the server did not respond. This does not mean there is no license.",
  },
  retry: { ru: "Повторить", en: "Retry" },
  statusLabel: { ru: "Статус:", en: "Status:" },
  badgeNotSet: { ru: "Не задана", en: "Not set" },
  badgeActive: { ru: "Активна", en: "Active" },
  badgeNotYet: { ru: "Ещё не действует", en: "Not yet active" },
  badgeExpired: { ru: "Истекла", en: "Expired" },
  notConfigured: {
    ru: "Лицензия не установлена — сервер работает в редакции Free.",
    en: "No license installed — the server is running the Free edition.",
  },
  expiredMsg: {
    ru: "Срок действия закончился, enterprise-функции отключены. Данные не затронуты: после применения новой лицензии всё вернётся.",
    en: "The validity period has ended, enterprise features are disabled. Data is unaffected: everything comes back once a new license is applied.",
  },
  notYetMsg: {
    ru: "Период действия ещё не начался, поэтому enterprise-функции пока выключены. Если дата уже должна была наступить — проверьте часы сервера.",
    en: "The validity period has not started yet, so enterprise features are off for now. If the date should already have arrived, check the server clock.",
  },
  graceMsg: {
    ru: "Срок истёк, функции пока работают на отсрочке — продлите лицензию.",
    en: "The term has expired; features are running on a grace period — renew the license.",
  },
  licensee: { ru: "Кому выдана: ", en: "Issued to: " },
  edition: { ru: "Редакция: ", en: "Edition: " },
  features: { ru: "Функции: ", en: "Features: " },
  seats: { ru: "Устройств по договору: ", en: "Contracted devices: " },
  validUntil: { ru: "Действует до: ", en: "Valid until: " },
  daysRemaining: { ru: " — осталось {n} дн.", en: " — {n} days left" },
  replaceLicense: { ru: "Заменить лицензию", en: "Replace license" },
  applyLicense: { ru: "Применить лицензию", en: "Apply license" },
  licenseKey: { ru: "Лицензионный ключ", en: "License key" },
  keyPlaceholder: {
    ru: "eyJwYXlsb2FkIjoi... — одна строка base64, как её выдал routineops-license",
    en: "eyJwYXlsb2FkIjoi... — a single base64 line, as issued by routineops-license",
  },
  activationPassword: { ru: "Пароль активации", en: "Activation password" },
  applyHint: {
    ru: "Применяется сразу, без перезапуска сервера. Отклонённый ключ не сбрасывает текущую лицензию.",
    en: "Applied immediately, without restarting the server. A rejected key does not reset the current license.",
  },
  applying: { ru: "Применение...", en: "Applying..." },
  apply: { ru: "Применить", en: "Apply" },
  deactivate: { ru: "Деактивировать", en: "Deactivate" },
  confirmDeactivateTitle: { ru: "Деактивировать лицензию?", en: "Deactivate the license?" },
  confirmDeactivateDesc: {
    ru: "Сервер сразу перейдёт в редакцию Free: enterprise-функции отключатся, ключ будет удалён с диска. Данные не удаляются, лицензию можно применить снова.",
    en: "The server switches to the Free edition immediately: enterprise features turn off and the key is deleted from disk. Data is not deleted; the license can be applied again.",
  },
}

// Порог «скоро истечёт»: за месяц до конца срока продление ещё успевает пройти
// по обычному закупочному циклу, поэтому предупреждаем заранее, а не в последний день.
const EXPIRY_WARN_DAYS = 30

function daysUntil(iso: string): number {
  return Math.ceil((new Date(iso).getTime() - Date.now()) / 86_400_000)
}

// hasExpiry: encoding/json ИГНОРИРУЕТ omitempty на time.Time (это структура), поэтому
// сервер всегда присылает expires_at — у лицензии без срока там нулевое время
// "0001-01-01T00:00:00Z". Проверка на непустую строку такое не отсеет и отрисовала бы
// «Действует до 01.01.0001», поэтому смотрим на год.
function hasExpiry(iso?: string): iso is string {
  return !!iso && new Date(iso).getUTCFullYear() > 1
}

// featuresLabel: пустой список фич в лицензии означает «вся редакция целиком»
// (семантика Claims.Has на сервере), а не «ничего не разрешено» — показать здесь
// прочерк значило бы соврать ровно наоборот.
function featuresLabel(features?: string[]): string | null {
  return features?.length ? features.join(", ") : null
}

export default function License() {
  const t = useT()
  const [status, setStatus] = useState<LicenseStatus | null>(null)
  // Три исхода загрузки, а не два. status === null означает «неизвестно», и его нельзя
  // рендерить как «не задана»: на enterprise-сервере с живой лицензией любой 500/502
  // (например рестарт контейнера по update.sh) нарисовал бы админу уверенное
  // «лицензия не установлена, редакция Free». unavailable — штатное состояние
  // open-core (роута нет → 404), loadError — настоящий сбой.
  const [unavailable, setUnavailable] = useState(false)
  const [loadError, setLoadError] = useState(false)
  const [loading, setLoading] = useState(true)
  const [blob, setBlob] = useState("")
  const [password, setPassword] = useState("")
  const [submitting, setSubmitting] = useState(false)
  const [confirmDeactivate, setConfirmDeactivate] = useState(false)
  // persistWarning живёт в state, а не только в тосте: «применено, но не сохранено»
  // означает, что рестарт вернёт сервер к прежнему состоянию — такое нельзя показать
  // на три секунды и убрать. Висит баннером до следующего успешного действия.
  const [persistWarning, setPersistWarning] = useState("")

  async function load() {
    setLoadError(false)
    try {
      const r = await api.get<LicenseStatus>("/license")
      setStatus(r.data)
    } catch (e) {
      if (errStatus(e) === 404) setUnavailable(true)
      else {
        setLoadError(true)
        toast({ title: t(M.loadErr), variant: "destructive" })
      }
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    load()
  }, [])

  // submit шлёт и применение, и деактивацию (пустой blob = сброс до Free на сервере).
  // catch пустой намеренно: интерцептор уже показал текст сервера («лицензия отклонена:
  // ...»), а он информативнее любого нашего заголовка. Без catch отказ POST (штатный
  // путь — опечатка в ключе) уходил бы наверх необработанным rejection'ом.
  async function submit(license: string, activationPassword: string) {
    setSubmitting(true)
    try {
      const r = await api.post<LicenseStatus>("/license", {
        license,
        activation_password: activationPassword,
      })
      setStatus(r.data)
      setLoadError(false)
      setPersistWarning(r.data.persist_warning ?? "")
      setBlob("")
      setPassword("")
      // Успех HTTP ≠ успех по существу. Два случая, когда 200 означает проблему:
      // ключ не лёг на диск (рестарт всё откатит) и лицензия принята, но не в сроке
      // (подпись верна, а фичи не включились). Зелёный тост в этих случаях врал бы.
      if (r.data.persist_warning) {
        toast({
          title: license ? t(M.appliedNotSaved) : t(M.disabledNotDeleted),
          description: r.data.persist_warning,
          variant: "destructive",
        })
      } else if (license && !r.data.valid) {
        toast({
          title: t(M.acceptedNotInTerm),
          description: t(M.acceptedNotInTermDesc),
          variant: "destructive",
        })
      } else {
        toast({
          title: license ? t(M.licenseApplied) : t(M.licenseDeactivated),
          description: license ? t(M.licenseAppliedDesc) : t(M.licenseDeactivatedDesc),
          variant: "success",
        })
      }
    } catch {
      /* авто-тост интерцептора */
    } finally {
      setSubmitting(false)
    }
  }

  function handleApply(e: FormEvent) {
    e.preventDefault()
    submit(blob.trim(), password)
  }

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
          <p className="text-sm text-muted-foreground">
            {t(M.unavailableBody)}
          </p>
        </div>
      </div>
    )
  }

  const left = hasExpiry(status?.expires_at) ? daysUntil(status.expires_at) : null
  // «Истекла» и «ещё не действует» — разные состояния с одинаковым configured && !valid.
  // Второе бывает при отставших часах VM (см. ErrNotYet), и сказать про такую лицензию
  // «срок закончился» — послать админа искать не ту проблему.
  const notValid = !!status?.configured && !status.valid
  const notYet = notValid && left !== null && left > 0
  const expired = notValid && !notYet
  // Отсрочка: valid при уже прошедшей дате — работает ROUTINEOPS_LICENSE_GRACE.
  const inGrace = !!status?.valid && left !== null && left <= 0
  const expiringSoon = !!status?.valid && left !== null && left > 0 && left <= EXPIRY_WARN_DAYS

  return (
    <div className="flex flex-col gap-5 max-w-2xl">
      <h1 className="text-xl font-semibold text-foreground">{t(M.title)}</h1>

      {persistWarning && (
        <div className="glass bg-red-500/[0.08] px-5 py-[18px] text-sm text-destructive dark:text-[hsl(0_72%_66%)]">
          {persistWarning}
        </div>
      )}

      {loadError ? (
        <div className="glass px-5 py-[18px] space-y-3 text-sm">
          <p className="text-[15px] font-semibold text-foreground">{t(M.statusLoadErrTitle)}</p>
          <p className="text-muted-foreground">
            {t(M.statusLoadErrBody)}
          </p>
          <Button variant="outline" size="sm" onClick={load}>
            {t(M.retry)}
          </Button>
        </div>
      ) : (
        <div className="glass px-5 py-[18px] space-y-2 text-sm">
          <div className="flex items-center gap-2">
            <span className="text-soft">{t(M.statusLabel)}</span>
            {!status?.configured && <Badge variant="secondary">{t(M.badgeNotSet)}</Badge>}
            {status?.valid && <Badge variant="success">{t(M.badgeActive)}</Badge>}
            {notYet && <Badge variant="secondary">{t(M.badgeNotYet)}</Badge>}
            {expired && <Badge variant="destructive">{t(M.badgeExpired)}</Badge>}
          </div>

          {!status?.configured && (
            <p className="text-muted-foreground">
              {t(M.notConfigured)}
            </p>
          )}

          {expired && (
            <p className="text-destructive dark:text-[hsl(0_72%_66%)]">
              {t(M.expiredMsg)}
            </p>
          )}

          {notYet && (
            <p className="text-muted-foreground">
              {t(M.notYetMsg)}
            </p>
          )}

          {inGrace && (
            /* В светлой теме #f59e0b на стекле даёт ~2.2:1 — берём затемнённый
               той же тональности, в тёмной остаётся статусный amber. */
            <p className="text-[#b45309] dark:text-[#f59e0b]">
              {t(M.graceMsg)}
            </p>
          )}

          {status?.configured && (
            <>
              <div className="text-foreground">
                <span className="text-soft">{t(M.licensee)}</span>
                {status.licensee || "—"}
              </div>
              <div className="text-foreground">
                <span className="text-soft">{t(M.edition)}</span>
                {status.edition || "—"}
              </div>
              <div className="text-foreground">
                <span className="text-soft">{t(M.features)}</span>
                {featuresLabel(status.features) ?? t(M.wholeEdition)}
              </div>
              {status.seats ? (
                <div className="text-foreground">
                  <span className="text-soft">{t(M.seats)}</span>
                  {status.seats}
                </div>
              ) : null}
              {hasExpiry(status.expires_at) && (
                <div className={expiringSoon ? "text-[#b45309] dark:text-[#f59e0b]" : "text-foreground"}>
                  <span className={expiringSoon ? "" : "text-soft"}>{t(M.validUntil)}</span>
                  {new Date(status.expires_at).toLocaleDateString("ru-RU")}
                  {/* Срок словами, а не только жёлтым цветом: цвет как единственный
                      носитель смысла — это WCAG 1.4.1. */}
                  {left !== null && left > 0 && t(M.daysRemaining, { n: left })}
                </div>
              )}
            </>
          )}
        </div>
      )}

      <form onSubmit={handleApply} className="glass px-5 py-[18px] space-y-4">
        <h2 className="text-[15px] font-semibold text-foreground">
          {status?.configured ? t(M.replaceLicense) : t(M.applyLicense)}
        </h2>
        <div className="space-y-1.5">
          <Label htmlFor="license-blob" className="text-soft">{t(M.licenseKey)}</Label>
          <textarea
            id="license-blob"
            className="flex min-h-32 w-full rounded-md border border-input bg-transparent px-3 py-2 text-sm font-mono shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring resize-y"
            placeholder={t(M.keyPlaceholder)}
            value={blob}
            onChange={(e) => setBlob(e.target.value)}
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="license-password" className="text-soft">{t(M.activationPassword)}</Label>
          <Input
            id="license-password"
            type="password"
            autoComplete="off"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </div>
        <p className="text-xs text-muted-foreground">
          {t(M.applyHint)}
        </p>
        <div className="flex gap-2">
          <Button type="submit" disabled={submitting || !blob.trim() || !password}>
            {submitting ? t(M.applying) : t(M.apply)}
          </Button>
          {status?.configured && (
            <Button
              type="button"
              variant="outline"
              className="text-destructive border-destructive/30 hover:bg-destructive/10"
              disabled={submitting}
              onClick={() => setConfirmDeactivate(true)}
            >
              {t(M.deactivate)}
            </Button>
          )}
        </div>
      </form>

      <ConfirmDialog
        open={confirmDeactivate}
        onOpenChange={setConfirmDeactivate}
        title={t(M.confirmDeactivateTitle)}
        description={t(M.confirmDeactivateDesc)}
        confirmLabel={t(M.deactivate)}
        destructive
        onConfirm={() => submit("", "")}
      />
    </div>
  )
}
