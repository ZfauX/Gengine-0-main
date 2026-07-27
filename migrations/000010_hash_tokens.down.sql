-- 000010_hash_tokens.down.sql
-- Откат: ничего не делаем.
-- Колонка token_hash уже создана в 000001_init под этим именем.
-- Переименование в token сломало бы все запросы.
-- Если в БД осталась колонка token из более старой схемы — удаляем её.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name = 'password_reset_tokens' AND column_name = 'token') THEN
        ALTER TABLE password_reset_tokens DROP COLUMN token;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name = 'email_verification_tokens' AND column_name = 'token') THEN
        ALTER TABLE email_verification_tokens DROP COLUMN token;
    END IF;
END $$;
