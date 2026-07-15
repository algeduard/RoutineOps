package service

// Layout — стабильные пути установки агента-службы для платформ без инсталлятора
// (macOS/Linux: пользователь запускает скачанный бинарь, служба должна жить не в
// /tmp). На Windows установку делает MSI — там Relocate=false и пути пустые.
type Layout struct {
	Relocate bool   // перекладывать ли бинарь и серты в стабильные пути
	BinPath  string // куда положить бинарь (он же в ExecStart/ProgramArguments)
	DataDir  string // изменяемое состояние (outbox, *.seen, forbidden, lock)
	CertDir  string // mTLS-материал (cert/key/ca)
	LogDir   string // каталог логов демона
}
