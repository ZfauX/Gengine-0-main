#!/bin/sh
set -e

# Применяем миграции перед запуском сервера
echo "Running database migrations..."
./gengine -migrate

# Запускаем сервер
echo "Starting server..."
exec ./gengine