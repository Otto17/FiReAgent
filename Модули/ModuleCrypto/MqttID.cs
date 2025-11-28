// Copyright (c) 2025 Otto
// Лицензия: MIT (см. LICENSE)

using System;
using System.IO;
using System.Text.RegularExpressions;

namespace ModuleCrypto
{
    internal static class MqttID
    {
        // Путь к файлу конфигурации с ID
        private static readonly string MqttIdFilePath = Path.Combine(AppDomain.CurrentDomain.BaseDirectory, "config\\MqttID.conf");

        // GetOrCreateClientId получает существующий уникальный ID клиента или генерирует новый
        internal static string GetOrCreateClientId()
        {
            try
            {
                // Проверяет, существует ли файл с MQTT ID в папке
                if (File.Exists(MqttIdFilePath))
                {
                    // Считывает ID из файла
                    string storedId = File.ReadAllText(MqttIdFilePath).Trim();

                    // Проверка корректности ID (не пустой, не длиннее 23 символов, допустимые символы)
                    if (!string.IsNullOrEmpty(storedId) && storedId.Length <= 23 && IsValidClientId(storedId))
                    {
                        // Проверяет, соответствует ли первая часть ID текущему имени компьютера
                        string[] idParts = storedId.Split(['_'], 2);
                        if (idParts.Length >= 1 && idParts[0] == Environment.MachineName)
                        {
                            return storedId;
                        }
                        else
                        {
                            string oldMachineName = idParts.Length >= 1 ? idParts[0] : "[неизвестно]";

                            // Если ID некорректен, удаляет файл
                            File.Delete(MqttIdFilePath);
                            Logging.WriteToLogFile($"Обнаружено изменение имени компьютера с '{oldMachineName}' на '{Environment.MachineName}'. Старый MQTT ID удален.");
                        }
                    }
                    else
                    {
                        File.Delete(MqttIdFilePath);
                        Logging.WriteToLogFile($"Mqtt ID некорректен или поврежден, файл \"{MqttIdFilePath}\" удален");
                    }
                }

                // Генерирует новый ID, если существующий не найден или был некорректен
                string newId = GenerateNewClientId();
                File.WriteAllText(MqttIdFilePath, newId);
                Logging.WriteToLogFile($"Новый ID сгенерирован и сохранен: {newId}");
                return newId;
            }
            catch (Exception ex)
            {
                // Логирует ошибку
                Logging.WriteToLogFile($"Ошибка при работе с файлом MqttID.conf: {ex.Message}");
                return GenerateNewClientId(); // Генерирует новый ID, в случае ошибки доступа к файлу
            }
        }

        // GenerateNewClientId генерирует новый уникальный ID клиента, используя имя машины и часть GUID (не более 23 символов)
        private static string GenerateNewClientId()
        {
            return $"{Environment.MachineName}_{Guid.NewGuid().ToString().Substring(0, 7)}";
        }

        // IsValidClientId проверяет корректность ID (только буквы, цифры, тире, длинное тире, подчёркивание)
        private static bool IsValidClientId(string clientId)
        {
            // Разрешённые символы: буквы, цифры, тире "–", длинное тире "—", подчёркивание "_"
            return Regex.IsMatch(clientId, @"^[a-zA-Z0-9_\-\—]+$");
        }
    }
}
