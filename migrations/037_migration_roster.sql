-- 037: миграция парка из другого MDM — импортированный ростер «ожидаемых» устройств.
-- Админ выгружает список уже управляемого парка из старого MDM (CSV) и заливает сюда.
-- Ростер — ЧИСТО СПРАВОЧНЫЙ оверлей: он НЕ создаёт устройства, НЕ меняет статусы и НЕ
-- открывает доступ. Реальные устройства по-прежнему заезжают через bulk-энролл и
-- проходят человеческую очередь одобрения. Матчинг «ожидаемое ↔ приехавшее» считается
-- НА ЧТЕНИИ (по serial, запасной ключ — hostname), поэтому колонки matched здесь нет:
-- ростер ничего не знает о devices, связь живёт только в SELECT (см. ListMigrationRoster).
-- Так импорт файла физически не может задеть горячий путь энролла.
CREATE TABLE device_migration_roster (
  id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  batch_label   TEXT NOT NULL DEFAULT '',   -- метка партии импорта (напр. "Intune 2026-07")
  hostname      TEXT NOT NULL DEFAULT '',
  serial_number TEXT NOT NULL DEFAULT '',   -- сильный ключ матча (админ сверяет с железом)
  assigned_user TEXT NOT NULL DEFAULT '',   -- закреплённый сотрудник из старого MDM
  asset_tag     TEXT NOT NULL DEFAULT '',   -- инвентарный номер
  group_hint    TEXT NOT NULL DEFAULT '',   -- имя целевой группы (человекочитаемое, справочно)
  notes         TEXT NOT NULL DEFAULT '',
  source_mdm    TEXT NOT NULL DEFAULT '',   -- откуда мигрируем (Intune / Kandji / …)
  imported_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  imported_by   TEXT NOT NULL DEFAULT ''    -- email админа, кто залил партию
);

-- Идемпотентность повторной заливки того же CSV: одна и та же машина в той же партии не
-- задваивается. Ключ идентичности ОБЯЗАН совпадать с ключом матча (serial — сильный,
-- hostname — запасной), иначе машина, приехавшая с разными hostname под одним serial,
-- задвоится (её всё равно матчат по serial). Поэтому ДВА частичных уникальных индекса,
-- а не один составной:
--   • есть серийник → машина уникальна по (batch, serial), hostname не в ключе;
--   • серийника нет → по (batch, hostname).
-- Регистронезависимо (serial/hostname из разных MDM приходят в разном регистре). Строки,
-- где и hostname, и serial пусты, ImportMigrationRoster отбрасывает ДО вставки.
CREATE UNIQUE INDEX uq_migration_roster_serial
  ON device_migration_roster (batch_label, lower(serial_number)) WHERE serial_number <> '';
CREATE UNIQUE INDEX uq_migration_roster_hostname
  ON device_migration_roster (batch_label, lower(hostname)) WHERE serial_number = '';

-- Обратный матч (карточка устройства, MigrationRosterForDevice) ищет строку ростера по
-- серийнику/hostname устройства — эти индексы на ростере его ускоряют.
CREATE INDEX idx_migration_roster_serial
  ON device_migration_roster (lower(serial_number)) WHERE serial_number <> '';
CREATE INDEX idx_migration_roster_hostname
  ON device_migration_roster (lower(hostname)) WHERE hostname <> '';

-- Прямой матч (ListMigrationRoster) для КАЖДОЙ строки ростера ищет устройство по
-- lower(serial)/lower(hostname). Без функциональных индексов на самих devices это seq-scan
-- всего парка на каждую строку ростера — O(ростер × парк). Индексы кладём на devices
-- (в 018 serial_number добавлен без индекса, hostname с 001 тоже неиндексирован).
-- Выражение индекса ДОЛЖНО в точности совпадать с предикатом матча (без COALESCE), иначе
-- планировщик его не использует — поэтому в matchJoin стоит голый lower(d.serial_number).
CREATE INDEX idx_devices_serial_lower ON devices (lower(serial_number));
CREATE INDEX idx_devices_hostname_lower ON devices (lower(hostname));
