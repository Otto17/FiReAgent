// Copyright (c) 2025-2026 Otto
// Лицензия: MIT (см. LICENSE)

using System;
using System.IO;
using System.Text;

namespace ModuleInfo
{
    // Logging предоставляет функциональность для записи сообщений в лог-файл
    internal class Logging
    {
        private static readonly string PATH_LOG = Path.Combine(AppDomain.CurrentDomain.BaseDirectory, "log");  // Указывает путь для хранения лог-файлов
        private const int MAX_LOG_SIZE = 1000000; // Максимальный размер лог-файла для ротации в байтах. (Установлено 1 Мбайт)
        private const int MAX_LOG_FILES = 2;      // Максимальное количество архивных лог-файлов. Имеют суффиксы (_0 и _1)

        // WriteToLogFile записывает сообщение в текущий лог-файл
        internal static void WriteToLogFile(string message)
        {
            try
            {
                // Создаёт директорию логов, если она еще не существует
                if (!Directory.Exists(PATH_LOG))
                {
                    Directory.CreateDirectory(PATH_LOG);
                }

                // Формирует полный путь к лог-файлу
                string logFilePath = Path.Combine(PATH_LOG, "log_ModuleInfo.txt");

                // Вызывает ротацию, чтобы текущий лог не превышал лимит размера
                if (File.Exists(logFilePath) && new FileInfo(logFilePath).Length >= MAX_LOG_SIZE)
                {
                    RotateLogFiles();
                }

                // Использует StreamWriter в режиме добавления, чтобы не перезаписывать файл
                using StreamWriter sw = new(logFilePath, true, Encoding.UTF8);
                sw.WriteLine($"{DateTime.Now:dd.MM.yyг. в HH:mm:ss}: {message}"); // Создаёт формат времени "дд.мм.гг в ЧЧ:ММ:СС"
            }
            catch (Exception)
            {
                // В случае ошибки логирования, ничего не делает
            }
        }

        // RotateLogFiles управляет ротацией лог-файлов при превышении размера
        private static void RotateLogFiles()
        {
            try
            {
                // Удаляет самый старый лог, если превышен лимит по объёму файла
                string oldestLogFile = Path.Combine(PATH_LOG, $"log_ModuleInfo_{MAX_LOG_FILES - 1}.txt");
                if (File.Exists(oldestLogFile))
                {
                    File.Delete(oldestLogFile);
                }

                // Перемещает архивные логи на одну позицию вверх
                for (int i = MAX_LOG_FILES - 1; i > 0; i--)
                {
                    string oldFile = Path.Combine(PATH_LOG, $"log_ModuleInfo_{i - 1}.txt");
                    string newFile = Path.Combine(PATH_LOG, $"log_ModuleInfo_{i}.txt");

                    if (File.Exists(oldFile))
                    {
                        File.Move(oldFile, newFile);
                    }
                }

                // Перемещает текущий лог в первый архивный лог
                string currentLogFile = Path.Combine(PATH_LOG, "log_ModuleInfo.txt");
                string newLogFile = Path.Combine(PATH_LOG, "log_ModuleInfo_0.txt");

                if (File.Exists(currentLogFile))
                {
                    File.Move(currentLogFile, newLogFile);
                }
            }
            catch (Exception)
            {
                // В случае ошибки ротации логов, ничего не делает
            }
        }
    }
}
