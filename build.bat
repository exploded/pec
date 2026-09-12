@echo off
rem Build pec: vet, test, then compile pec.exe. Run "pec.exe serve" afterwards.
go vet ./...
if errorlevel 1 (echo vet failed & exit /b 1)
go test ./...
if errorlevel 1 (echo tests failed & exit /b 1)
go build -o pec.exe ./cmd/pec
if errorlevel 1 (echo build failed & exit /b 1)
echo built pec.exe
