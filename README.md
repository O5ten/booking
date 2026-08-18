# booking.rudbeckia.nu

Bokningssystem för Kollektivhuset Rudbeckia. Husets medlemmar bokar cyklar,
gästrum och lokaler bakom ett gemensamt lösenord, får en bekräftelse på mejlen
och kan lägga in bokningen i sin egen kalender.

Byggt som en enda statisk Go-binär med SQLite. Ingen databasserver, ingen
byggkedja för frontend, inget att uppdatera utöver containern.

---

## Prova på

Vill du bara se hur det ser ut? Starta en demo. Den behöver ingen konfiguration,
inga lösenord och ingen databas — och den fyller sig själv med påhittade
bokningar så att du har något att klicka på.

**Med Docker** — det enda du behöver är Docker:

```bash
docker run --rm -p 8080:8080 -e DEMO=true \
    ghcr.io/mikaelo/booking.rudbeckia.nu:latest
```

**Med Go** — om du klonat repot:

```bash
make demo          # eller: go run ./cmd/server -demo
```

**Med repot och Docker** — bygger från källkoden om bilden inte går att hämta:

```bash
docker compose -f docker-compose.demo.yml up
```

Öppna sedan <http://localhost:8080>.

| | |
|---|---|
| Lösenord | `demo` — redan ifyllt i formuläret |
| Administratör | `admin` — visar allas bokningar under **Alla bokningar** |

Demon säger tydligt ifrån med en banner på varje sida, och all data ligger i
minnet: stoppar du containern är allt borta. Kör aldrig `DEMO=true` skarpt —
lösenorden står ju på sidan.

Saker att testa: boka en cykel och se hur tiderna runtomkring stängs av
[pausen mellan bokningar](#alla-parametrar), försök boka samma tid två gånger,
boka ett gästrum över några nätter, ladda ner kalenderfilen från bekräftelsen,
och logga in som `admin` för att se hela husets bokningar och exportera dem.

---

## Kom igång på riktigt

```bash
cp .env.example .env
$EDITOR .env                 # sätt åtminstone BOOKING_PASSWORD
docker compose up -d
```

Sidan ligger på <http://localhost:8080>. Logga in med `BOOKING_PASSWORD`.

Vill du köra utan Docker:

```bash
BOOKING_PASSWORD=hemligt go run ./cmd/server
```

Alla vardagskommandon finns i `Makefile` — kör `make` för att se dem.

---

## Vad som går att boka

Allt bokningsbart står i `config.yaml`. Filen läses vid start, så starta om
containern när du ändrat något. Kontrollera först att filen är giltig:

```bash
docker compose run --rm booking -check-config
```

### Två sorters resurser

**`mode: hours`** – saker man lånar en stund, som cyklar. Medlemmen väljer dag,
längd och starttid ur ett rutnät.

```yaml
- id: ellastcykel
  category: cyklar
  name: Ellastcykeln
  emoji: 🚲
  description: Elektrisk lastcykel med plats för barn eller storhandling.
  location: Cykelrummet i källaren
  instructions: Nyckeln sitter i nyckelskåpet vid torget.
  booking:
    mode: hours
    durations: [1, 2, 4, 8]        # tillåtna längder i timmar
    slot_step_minutes: 30          # starttider ligger på halvtimmen
    buffer_minutes: 15             # tvingande lucka mellan två bokningar
    open_from: "06:00"
    open_to: "22:00"
    max_advance_days: 30
    min_notice_minutes: 0          # hur nära inpå man får boka
    max_active_per_user: 2         # samtidiga bokningar per person
    max_hours_per_week_per_user: 16
```

**`mode: days`** – saker man bokar över natten, som gästrum. Medlemmen väljer
in- och utcheckningsdatum i en månadskalender.

```yaml
- id: gastrum-1
  category: gastrum
  name: Gästrum 1
  booking:
    mode: days
    min_days: 1                    # kortaste bokning i nätter
    max_days: 7
    check_in: "15:00"
    check_out: "12:00"
    max_advance_days: 180
    max_active_per_user: 2
```

### Lägga till en ny resurs

Lägg till ett block under `resources`, med ett `id` som aldrig ändras (det står
i länkar och i databasen). Sätt `enabled: false` för att dölja något utan att ta
bort det — bokningar som redan finns påverkas inte.

`config.yaml` innehåller redan matsalen och snickarverkstaden avstängda, som
utgångspunkt när huset vill boka fler saker digitalt.

### Alla parametrar

| Nyckel | Gäller | Betyder |
|---|---|---|
| `mode` | båda | `hours` eller `days` |
| `durations` | hours | tillåtna längder i timmar |
| `slot_step_minutes` | hours | rutnätet starttider ligger på |
| `open_from`, `open_to` | hours | den del av dygnet som får bokas |
| `min_days`, `max_days` | days | kortaste och längsta bokning i nätter |
| `check_in`, `check_out` | days | klockslag för in- och utcheckning |
| `buffer_minutes` | båda | tvingande fri tid mellan två bokningar |
| `max_advance_days` | båda | hur långt fram i tiden man får boka |
| `min_notice_minutes` | båda | hur nära inpå man får boka |
| `max_active_per_user` | båda | samtidiga bokningar per e-postadress |
| `max_hours_per_week_per_user` | hours | takt-tak per person, 0 = inget tak |

---

## Inställningar

Allt hemligt sätts som miljövariabler, aldrig i `config.yaml`. Se
[`.env.example`](.env.example) för hela listan.

| Variabel | Standard | Betyder |
|---|---|---|
| `BOOKING_PASSWORD` | – | **Krävs.** Husets gemensamma lösenord |
| `BASE_URL` | `http://localhost:8080` | Adressen som hamnar i mejl och kalenderlänkar |
| `ADMIN_PASSWORD` | tom | Låser upp `/admin`. Tom = ingen adminvy |
| `SESSION_SECRET` | härleds | Signerar sessionskakor |
| `SESSION_DAYS` | `30` | Hur länge en inloggning håller |
| `CONFIG_PATH` | `/config.yaml` | Var resursfilen ligger |
| `DB_PATH` | `/data/booking.db` | Var databasen ligger |
| `LISTEN_ADDR` | `:8080` | Adress att lyssna på |
| `TRUST_PROXY` | `true` | Läs klientens IP ur `X-Forwarded-For` |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` eller `error` |
| `SMTP_HOST` m.fl. | tom | Utan dessa loggas bekräftelserna i stället för att skickas |
| `DEMO` | `false` | Demoläge: lösenorden `demo`/`admin`, exempelbokningar och en banner. Aldrig i skarp drift |

Sätter du inte `SESSION_SECRET` härleds den ur lösenorden. Det betyder att ett
byte av husets lösenord loggar ut alla — praktiskt när någon flyttar ut.

---

## Så fungerar det

**Ett lösenord, inga konton.** Hela sajten ligger bakom `BOOKING_PASSWORD`.
Medlemmen skriver sitt namn och sin e-post när hen bokar; uppgifterna sparas i
en signerad kaka så nästa bokning går fortare. `ADMIN_PASSWORD` ger dessutom
`/admin` med alla bokningar, filter och CSV-export.

**Bekräftelse på mejlen.** När bokningen gått igenom skickas ett mejl med en
bifogad `.ics`-fil (Apple Calendar, Outlook, Thunderbird) och direktlänkar till
Google Calendar och Outlook på webben. Mejlet innehåller också länken för att
avboka.

**Kalenderflöden.** Varje resurs publicerar sina bokningar på
`/kalender/<id>/flode.ics`, om huset vill visa dem i en gemensam kalender.

**Dubbelbokning är omöjlig.** Kollisionskontrollen och skrivningen sker i samma
transaktion, med `BEGIN IMMEDIATE`, så två personer som klickar på samma tid i
samma sekund inte båda kan vinna. Den som förlorar får ett vänligt felmeddelande
och behåller sina ifyllda uppgifter.

---

## Bakom en proxy

Sätt `BASE_URL` till den riktiga adressen — den styr länkarna i mejlen, och den
avgör om sessionskakan sätts som `Secure` (den blir det när adressen är
`https://`). Exempel för Caddy:

```
booking.rudbeckia.nu {
    reverse_proxy booking:8080
}
```

Nginx behöver skicka vidare `X-Forwarded-For` för att inloggningsspärren ska
räkna rätt IP-adress.

---

## Deploy

Varje push till `main` bygger en image och lägger den i GitHub Packages, för
både `amd64` och `arm64`. Testerna körs först — går de inte igenom publiceras
ingenting.

```
ghcr.io/mikaelo/booking.rudbeckia.nu:latest
ghcr.io/mikaelo/booking.rudbeckia.nu:sha-<commit>
ghcr.io/mikaelo/booking.rudbeckia.nu:1.2.3   # från en v-tagg
```

Uppdatera på servern:

```bash
docker compose pull && docker compose up -d
```

Första gången du hämtar från ett privat paket:

```bash
echo $GITHUB_TOKEN | docker login ghcr.io -u <användarnamn> --password-stdin
```

---

## Säkerhetskopiering

Allt ligger i en enda SQLite-fil i volymen `booking-data`.

```bash
docker compose exec booking /booking -version   # kolla att den lever
docker run --rm -v booking-data:/data -v "$PWD:/backup" alpine \
    cp /data/booking.db /backup/booking-$(date +%F).db
```

Databasen körs i WAL-läge, så ta med `-wal`-filen om du kopierar en igång
varande databas — eller stoppa containern först.

---

## Utveckling

```bash
go test ./...          # alla tester
go test -race ./...    # med kapplöpningsdetektor
go vet ./...
BOOKING_PASSWORD=hemligt ADMIN_PASSWORD=admin go run ./cmd/server
```

| Paket | Ansvar |
|---|---|
| `internal/config` | Läser `config.yaml` och miljövariabler |
| `internal/store` | SQLite: bokningar, kollisioner, sökning |
| `internal/booking` | Räknar ut lediga tider och validerar en bokning |
| `internal/auth` | Lösenordsgrind, signerade kakor, tokens |
| `internal/mail` | Bygger och skickar MIME-mejlen |
| `internal/ical` | `.ics`-filer och kalenderlänkar |
| `internal/web` | Rutter, HTML-mallar, CSS |
| `internal/demo` | Exempelbokningar för demoläget |

Mallar och statiska filer bäddas in i binären med `go:embed`, så det finns bara
en fil att flytta runt.
