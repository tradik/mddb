# Szybki Start - MDDB Panel

## Czym jest MDDB Panel?

MDDB Panel to nowoczesny interfejs webowy do zarządzania bazą danych MDDB. Pozwala przeglądać kolekcje, dokumenty, filtrować po metadanych - wszystko w przeglądarce, bez linii poleceń.

## Instalacja i Uruchomienie

### Opcja 1: Docker Compose (Najłatwiejsza)

```bash
# Uruchom serwer i panel jednocześnie
docker-compose up -d

# Panel dostępny na: http://localhost:3000
# API serwera na: http://localhost:11023
```

### Opcja 2: Lokalnie (Development)

```bash
# Zainstaluj zależności
make panel-install

# Uruchom panel w trybie deweloperskim
make panel-dev

# Panel dostępny na: http://localhost:3000
```

### Opcja 3: Ręcznie

```bash
# Przejdź do katalogu panelu
cd services/mddb-panel

# Zainstaluj zależności
npm install

# Uruchom serwer deweloperski
npm run dev
```

## Pierwsze Kroki

### 1. Upewnij się że serwer MDDB działa

```bash
# Sprawdź status
curl http://localhost:11023/v1/stats

# Lub uruchom serwer
make docker-up
```

### 2. Otwórz Panel

Przejdź do http://localhost:3000 w przeglądarce

### 3. Przeglądaj Kolekcje

- **Sidebar (lewa strona)**: Lista wszystkich kolekcji
- **Statystyki**: Liczba dokumentów, rewizji, rozmiar bazy
- **Kliknij kolekcję**: Aby zobaczyć dokumenty

### 4. Przeglądaj Dokumenty

- **Lista dokumentów**: Klucz, język, data, metadane
- **Kliknij dokument**: Aby zobaczyć pełną treść
- **Przycisk "Copy"**: Kopiuje markdown do schowka

### 5. Filtruj Dokumenty

1. Kliknij przycisk **"Filters"** w górnym pasku
2. Dodaj filtry metadanych:
   - Klucz: np. "author"
   - Wartość: np. "Jan Kowalski"
3. Wybierz sortowanie i limit
4. Kliknij **"Apply Filters"**

## Funkcje

### 📊 Dashboard Statystyk
- Liczba dokumentów i rewizji
- Rozmiar bazy danych
- Lista kolekcji z licznikami

### 📁 Przeglądarka Kolekcji
- Wszystkie kolekcje w jednym miejscu
- Szybkie przełączanie między kolekcjami
- Liczba dokumentów w każdej kolekcji

### 📄 Zarządzanie Dokumentami
- Lista dokumentów z podglądem metadanych
- Pełna treść markdown
- Wszystkie metadane
- **Edycja dokumentów** - Modyfikuj treść i metadane
- **Edytor markdown z podglądem** - Widok podzielony z renderowaniem na żywo
- **Pasek narzędzi** - Szybkie formatowanie (pogrubienie, kursywa, nagłówki, listy)
- **Podświetlanie składni** - Bloki kodu z obsługą 100+ języków
- **Szablony** - Gotowe szablony (blog, dokumentacja, README, API)
- **Tworzenie nowych dokumentów** - Dodawaj dokumenty z UI
- Informacje o rewizjach

### 🔍 Zaawansowane Filtrowanie
- Filtruj po dowolnych metadanych
- Sortuj po dacie lub kluczu
- Rosnąco lub malejąco
- Limit wyników (1-1000)

### 🎨 Nowoczesny UI
- Czysty, responsywny design
- TailwindCSS
- Ikony Lucide React
- Płynne animacje

## Przykłady Użycia

### Znajdź wszystkie posty autora

1. Wybierz kolekcję "blog"
2. Kliknij "Filters"
3. Dodaj filtr: `author` = `Jan Kowalski`
4. Kliknij "Apply Filters"

### Pokaż najnowsze dokumenty

1. Wybierz kolekcję
2. Kliknij "Filters"
3. Sortuj po: "Updated Date"
4. Kolejność: "Descending"
5. Limit: 10
6. Kliknij "Apply Filters"

### Skopiuj treść dokumentu

1. Kliknij na dokument w liście
2. Kliknij przycisk "Copy" w prawym górnym rogu
3. Treść markdown jest w schowku

### Edytuj dokument

1. Kliknij na dokument w liście
2. Kliknij przycisk "Edit" 
3. Użyj edytora markdown:
   - **Pasek narzędzi** - Kliknij przyciski do formatowania:
     - Nagłówki (H1, H2, H3)
     - Pogrubienie, kursywa, przekreślenie
     - Kod inline
     - Linki, listy, tabele
   - **Szablony** - Wybierz gotowy szablon:
     - Blog Post
     - Documentation
     - README
     - API Documentation
     - Changelog
   - **Tryby widoku**:
     - **Edit** - Pisz markdown
     - **Preview** - Zobacz renderowany wynik z podświetlaniem składni
     - **Split** - Edytuj i podglądaj jednocześnie (domyślnie)
     - **Fullscreen** - Tryb pełnoekranowy
4. Modyfikuj treść markdown i metadane
5. Kliknij "Save Changes"

### Utwórz nowy dokument

1. Wybierz kolekcję
2. Kliknij przycisk "New Document"
3. Wypełnij:
   - Klucz dokumentu (unikalny)
   - Język
   - Metadane (opcjonalnie)
   - Treść markdown
4. Kliknij "Create Document"

## Produkcja

### Build dla Produkcji

```bash
# Zbuduj aplikację
make panel-build

# Podgląd buildu produkcyjnego
make panel-preview
```

### Docker dla Produkcji

```bash
# Zbuduj obraz
cd services/mddb-panel
docker build -t mddb-panel .

# Uruchom kontener
docker run -d \
  -p 3000:3000 \
  -e VITE_MDDB_SERVER=http://mddb-server:11023 \
  mddb-panel
```

## Konfiguracja

### Zmiana URL Serwera

Utwórz plik `.env` w `services/mddb-panel/`:

```env
VITE_MDDB_SERVER=http://localhost:11023
```

Lub ustaw zmienną środowiskową:

```bash
export VITE_MDDB_SERVER=http://production-server:11023
```

## Technologie

- **React 19.1** - Framework UI
- **Vite 6** - Build tool
- **TailwindCSS 4** - Stylowanie
- **Zustand 5** - Zarządzanie stanem
- **Lucide React** - Ikony
- **date-fns 4** - Formatowanie dat
- **react-markdown 10** - Renderowanie markdown
- **remark-gfm 4** - GitHub Flavored Markdown
- **react-syntax-highlighter** - Podświetlanie składni kodu
- **prismjs** - Silnik podświetlania (100+ języków)

## Rozwiązywanie Problemów

### Panel nie startuje

```bash
# Usuń node_modules i zainstaluj ponownie
cd services/mddb-panel
rm -rf node_modules package-lock.json
npm install
npm run dev
```

### Nie można połączyć z serwerem

```bash
# Sprawdź czy serwer działa
curl http://localhost:11023/v1/stats

# Uruchom serwer
make docker-up
```

### Błąd buildu

```bash
# Sprawdź wersję Node.js (musi być 24.3+)
node --version

# Zaktualizuj zależności
npm update
```

## Następne Kroki

- Przeczytaj [pełną dokumentację](docs/PANEL.md)
- Zobacz [dokumentację API](docs/API.md)
- Sprawdź [przykłady](examples/)

## Wsparcie

Jeśli masz pytania lub problemy:
1. Sprawdź [dokumentację](docs/)
2. Zobacz [przykłady](examples/)
3. Otwórz issue na GitHub
