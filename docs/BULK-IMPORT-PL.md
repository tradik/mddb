# Przewodnik Importu Masowego

## Przegląd

Skrypt `load-md-folder.sh` umożliwia masowy import plików markdown z folderu do bazy danych MDDB. Jest idealny do migracji istniejącej dokumentacji, importu postów blogowych lub ładowania dużych kolekcji treści markdown.

## Podstawowe Użycie

### Prosty Import

Zaimportuj wszystkie pliki `.md` z folderu:

```bash
./scripts/load-md-folder.sh ./docs blog
```

To spowoduje:
1. Przeskanowanie folderu `./docs` w poszukiwaniu plików `.md`
2. Zaimportowanie ich do kolekcji `blog`
3. Użycie domyślnego języka `en_US`
4. Wygenerowanie kluczy z nazw plików

### Import Rekurencyjny

Przetwórz wszystkie podfoldery:

```bash
./scripts/load-md-folder.sh ./content articles --recursive
```

Lub użyj krótkiej formy:

```bash
./scripts/load-md-folder.sh ./content articles -r
```

### Własny Język

Określ inny kod języka:

```bash
./scripts/load-md-folder.sh ./docs-pl blog --lang pl_PL
```

Krótka forma:

```bash
./scripts/load-md-folder.sh ./docs-pl blog -l pl_PL
```

## Zaawansowane Użycie

### Dodawanie Metadanych

Dodaj własne metadane do wszystkich importowanych plików:

```bash
./scripts/load-md-folder.sh ./posts blog \
  --meta "author=Jan Kowalski" \
  --meta "status=opublikowany" \
  --meta "kategoria=tutorial"
```

Krótka forma:

```bash
./scripts/load-md-folder.sh ./posts blog \
  -m "author=Jan Kowalski" \
  -m "status=opublikowany"
```

### Tryb Testowy (Dry Run)

Podejrzyj co zostanie zaimportowane bez wprowadzania zmian:

```bash
./scripts/load-md-folder.sh ./docs blog --dry-run
```

To pokaże:
- Które pliki zostaną zaimportowane
- Wygenerowane klucze
- Wyodrębnione metadane
- Końcową kombinację metadanych

### Szczegółowe Wyjście

Zobacz szczegółowe informacje podczas importu:

```bash
./scripts/load-md-folder.sh ./docs blog --verbose
```

Pokazuje:
- Każdy przetwarzany plik
- Wygenerowany klucz dla każdego pliku
- Metadane dla każdego pliku
- Status sukcesu/niepowodzenia

## Wsparcie dla Frontmatter

Skrypt automatycznie wyodrębnia metadane z frontmatter w stylu YAML:

```markdown
---
title: Pierwsze Kroki
author: Jan Kowalski
tags: tutorial, początkujący
kategoria: dokumentacja
data: 2024-01-15
---

# Pierwsze Kroki

Twoja treść tutaj...
```

Ten frontmatter zostanie przekonwertowany na metadane:
- `title=Pierwsze Kroki`
- `author=Jan Kowalski`
- `tags=tutorial, początkujący`
- `kategoria=dokumentacja`
- `data=2024-01-15`

## Generowanie Kluczy

Klucze są automatycznie generowane z nazw plików:

| Nazwa Pliku | Wygenerowany Klucz |
|-------------|-------------------|
| `Pierwsze Kroki.md` | `pierwsze-kroki` |
| `API_Dokumentacja.md` | `api-dokumentacja` |
| `2024-01-15-post-blog.md` | `2024-01-15-post-blog` |
| `Mój Dokument (v2).md` | `moj-dokument-v2` |

Zasady:
- Konwersja na małe litery
- Zamiana spacji i znaków specjalnych na myślniki
- Usunięcie kolejnych myślników
- Przycięcie myślników na początku/końcu

## Przykłady

### Migracja Dokumentacji

```bash
# Zaimportuj cały folder docs rekurencyjnie
./scripts/load-md-folder.sh ./docs dokumentacja \
  --recursive \
  --meta "wersja=2.0" \
  --meta "status=opublikowany" \
  --verbose
```

### Import Postów Blogowych

```bash
# Zaimportuj posty blogowe z metadanymi autora
./scripts/load-md-folder.sh ./posty-blog blog \
  --lang pl_PL \
  --meta "author=Jan Kowalski" \
  --meta "typ=post-blog"
```

### Treści Wielojęzyczne

```bash
# Zaimportuj wersję angielską
./scripts/load-md-folder.sh ./content/en artykuly -l en_US -r

# Zaimportuj wersję polską
./scripts/load-md-folder.sh ./content/pl artykuly -l pl_PL -r

# Zaimportuj wersję niemiecką
./scripts/load-md-folder.sh ./content/de artykuly -l de_DE -r
```

### Podgląd Przed Importem

```bash
# Najpierw wykonaj dry run
./scripts/load-md-folder.sh ./docs blog --dry-run

# Jeśli wszystko wygląda dobrze, wykonaj prawdziwy import
./scripts/load-md-folder.sh ./docs blog
```

## Użycie z Makefile

```bash
# Importuj folder
make import-folder FOLDER=./docs COLLECTION=blog

# Podgląd importu
make import-folder-dry FOLDER=./docs COLLECTION=blog

# Import rekurencyjny
make import-folder-recursive FOLDER=./docs COLLECTION=blog

# Z własnymi opcjami
make import-folder FOLDER=./docs COLLECTION=blog LANG=pl_PL META="author=Jan"
```

## Opcje

| Opcja | Opis | Domyślnie |
|-------|------|-----------|
| `-l, --lang LANG` | Kod języka | `en_US` |
| `-r, --recursive` | Przetwarzaj podfoldery rekurencyjnie | - |
| `-m, --meta KEY=VALUE` | Dodaj metadane (można użyć wielokrotnie) | - |
| `-s, --server URL` | URL serwera MDDB | `http://localhost:11023` |
| `-v, --verbose` | Szczegółowe wyjście | - |
| `-d, --dry-run` | Podgląd bez wykonywania | - |
| `-b, --batch-size N` | Częstotliwość aktualizacji postępu | `10` |
| `-h, --help` | Pokaż pomoc | - |

## Wymagania

- Powłoka Bash
- Polecenie `mddb-cli` dostępne w PATH
- Działający serwer MDDB

## Zmienne Środowiskowe

- `MDDB_SERVER` - URL serwera (domyślnie: http://localhost:11023)
- `MDDB_CLI` - Ścieżka do polecenia CLI (domyślnie: mddb-cli)

## Wyjście

### Wyświetlanie Postępu

```
════════════════════════════════════════════════
  MDDB Folder Loader
════════════════════════════════════════════════

Sprawdzanie połączenia z serwerem...
✓ Serwer działa

Konfiguracja:
  Folder:     ./docs
  Kolekcja:   blog
  Język:      pl_PL
  Serwer:     http://localhost:11023
  Rekurencyjnie: true

Skanowanie plików markdown...
Znaleziono 150 plików markdown

════════════════════════════════════════════════
  Ładowanie Plików
════════════════════════════════════════════════

Postęp: [##########################            ] 52% (78/150 plików)
```

### Podsumowanie

```
════════════════════════════════════════════════
  Podsumowanie
════════════════════════════════════════════════

Wyniki:
  Wszystkich plików:  150
  Udanych:            148
  Nieudanych:         2
  Czas trwania:       45s
  Przepustowość:      3.29 plików/sek

✓ Import zakończony z pewnymi błędami
```

## Rozwiązywanie Problemów

### Skrypt nie jest wykonywalny

```bash
chmod +x scripts/load-md-folder.sh
```

### CLI nie znaleziono

```bash
# Zainstaluj CLI
make build-cli
make install-all

# Lub określ pełną ścieżkę
MDDB_CLI=/sciezka/do/mddb-cli ./scripts/load-md-folder.sh ./docs blog
```

### Odmowa połączenia z serwerem

```bash
# Sprawdź czy serwer działa
mddb-cli stats

# Uruchom serwer
make docker-up
# lub
make run
```

## Najlepsze Praktyki

1. **Zawsze najpierw wykonaj dry run** na danych produkcyjnych
2. **Używaj znaczących nazw kolekcji** które odzwierciedlają typ treści
3. **Dodawaj metadane wersji** do śledzenia zmian
4. **Używaj trybu rekurencyjnego** dla zorganizowanych struktur folderów
5. **Dołączaj frontmatter** w plikach markdown dla bogatych metadanych
6. **Testuj z małymi partiami** przed dużymi importami
7. **Monitoruj zasoby serwera** podczas dużych importów
8. **Twórz kopie zapasowe bazy** przed większymi importami

## Zobacz Również

- [Dokumentacja CLI](CLI.md)
- [Dokumentacja API](API.md)
- [Przykłady](EXAMPLES.md)
- [Przewodnik Wdrożenia](DEPLOYMENT.md)
- [Pełna dokumentacja (EN)](BULK-IMPORT.md)
