-- 041: колонки под задачу удаления ПО (task_type='remove_software', enterprise-фича
-- «удаление ПО из интерфейса»). Несут продукт к тихой деинсталляции. У остальных типов
-- задач пустые (NOT NULL DEFAULT '', как lock_*-поля в 013), поэтому существующие
-- INSERT/RETURNING их не задают. Сама задача гейтится лицензией на уровне API.
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS software_name    TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS software_version TEXT NOT NULL DEFAULT '';
