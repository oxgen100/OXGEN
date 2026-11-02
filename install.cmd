@echo off
powershell -NoProfile -ExecutionPolicy Bypass -Command "irm 'https://raw.githubusercontent.com/oxgen100/OXGEN/main/install.ps1' | iex"
exit /b %errorlevel%