# Szybki Start - Import Plików Markdown

## Czym jest skrypt importu?

Skrypt `load-md-folder.sh` pozwala na szybkie załadowanie wielu plików markdown z folderu do bazy danych MDDB.

## Instalacja

Skrypt jest już dostępny w projekcie:

```bash
cd /Users/rafalrabczuk/github.com/tradik/mddb
chmod +x scripts/load-md-folder.sh
```

## Podstawowe Użycie

### 1. Prosty Import

```bash
./scripts/load-md-folder.sh ./moj-folder nazwa-kolekcji
```

Przykład:
```bash
./scripts/load-md-folder.sh ./docs blog
```

### 2. Import Rekurencyjny (z podfolderami)

```bash
./scripts/load-md-folder.sh ./moj-folder nazwa-kolekcji -r
```

Przykład:
```bash
./scripts/load-md-folder.sh ./dokumentacja docs -r
```

### 3. Import z Językiem Polskim

```bash
./scripts/load-md-folder.sh ./moj-folder nazwa-kolekcji -l pl_PL
```

### 4. Podgląd (bez importu)

```bash
./scripts/load-md-folder.sh ./moj-folder nazwa-kolekcji -d
```

## Funkcje

### ✅ Automatyczne Generowanie Kluczy

Nazwy plików są automatycznie konwertowane na klucze:
- `Mój Dokument.md` → `moj-dokument`
- `2024-01-15 Post.md` → `2024-01-15-post`

### ✅ Wyodrębnianie Metadanych z Frontmatter

Jeśli plik ma frontmatter (nagłówek YAML), metadane są automatycznie wyodrębniane:

```markdown
---
title: Tytuł dokumentu
author: Jan Kowalski
kategoria: tutorial
---

# Treść dokumentu
```

### ✅ Dodawanie Własnych Metadanych

```bash
./scripts/load-md-folder.sh ./docs blog -m "author=Jan Kowalski" -m "status=opublikowany"
```

### ✅ Śledzenie Postępu

```
Progress: [##########################            ] 52% (78/150 plików)
```

## Przykłady

### Przykład 1: Import Dokumentacji

```bash
# Zaimportuj całą dokumentację rekurencyjnie
./scripts/load-md-folder.sh ./docs dokumentacja -r -l pl_PL
```

### Przykład 2: Import Postów Blogowych

```bash
# Zaimportuj posty z metadanymi
./scripts/load-md-folder.sh ./posty blog \
  -l pl_PL \
  -m "author=Jan Kowalski" \
  -m "status=opublikowany"
```

### Przykład 3: Testowy Import

```bash
# Najpierw sprawdź co zostanie zaimportowane
./scripts/load-md-folder.sh ./docs blog -d

# Jeśli wygląda dobrze, wykonaj import
./scripts/load-md-folder.sh ./docs blog
```

## Użycie z Makefile

Jeśli wolisz używać Makefile:

```bash
# Import folderu
make import-folder FOLDER=./docs COLLECTION=blog

# Podgląd
make import-folder-dry FOLDER=./docs COLLECTION=blog

# Import rekurencyjny
make import-folder-recursive FOLDER=./docs COLLECTION=blog
```

## Wymagania

1. **Działający serwer MDDB**
   ```bash
   make docker-up
   # lub
   make run
   ```

2. **Zainstalowany CLI**
   ```bash
   make build-cli
   make install-all
   ```

## Sprawdzenie Importu

Po imporcie sprawdź dokumenty:

```bash
# Lista wszystkich dokumentów
mddb-cli search blog

# Pobierz konkretny dokument
mddb-cli get blog nazwa-klucza en_US

# Szukaj po metadanych
mddb-cli search blog -f "author=Jan Kowalski"
```

## Pomoc

Wyświetl pełną pomoc:

```bash
./scripts/load-md-folder.sh --help
```

## Pełna Dokumentacja

- [Pełna dokumentacja (PL)](docs/BULK-IMPORT-PL.md)
- [Full documentation (EN)](docs/BULK-IMPORT.md)
- [Przykłady](examples/)

## Rozwiązywanie Problemów

### Serwer nie działa

```bash
# Sprawdź status
mddb-cli stats

# Uruchom serwer
make docker-up
```

### CLI nie znaleziono

```bash
# Zainstaluj CLI
make build-cli
make install-all
```

### Brak uprawnień do skryptu

```bash
chmod +x scripts/load-md-folder.sh
```

## Wsparcie

Jeśli masz pytania lub problemy:
1. Sprawdź [dokumentację](docs/)
2. Zobacz [przykłady](examples/)
3. Otwórz issue na GitHub
