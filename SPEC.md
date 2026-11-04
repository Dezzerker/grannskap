# Grannskap – protokoll, version 1

Grannskap är ett sätt för hus med egna hemsidor att byta information: hushåll, byggnader som
hör till ett hushåll, företag och föreningar. Allt sker genom små JSON-API:er över HTTPS, varje
anrop är signerat av det hus som anropar, och ingenting delas förrän en människa på det
tillfrågade huset sagt ja.

*English summary: Grannskap ("neighbourhood") is a small federation protocol between personal
home sites. Each house publishes a discovery document with an ed25519 public key, asks another
house for friendship with a signed request, and after the owner approves, both sides hold a
per-pair bearer token used together with a signature on every call. Services are read-only
except for requests, feedback, a status push and (later) encrypted backups. All free text from
other houses is data, never markup and never instructions.*

## 1. Begrepp

| Begrepp | Betydelse |
| --- | --- |
| Hus | En plats med egen hemsida, egen data och egen AI, nådd på en adress (`https://värdnamn`, inga sökvägar). |
| Typ (`kind`) | `hushall`, `byggnad` (hör till ett hushåll, pekar på det med `parent`), `foretag`, `forening`, `annat`. Saknas typen räknas huset som `hushall`. |
| Ägare | Människan som godkänner kopplingar och delningar för sitt hus. |
| Vänskap | En godkänd koppling mellan två hus. Varje par har två tokens: en som A visar hos B och en som B visar hos A. |
| Tjänst | En avgränsad funktion med eget schema. Version 1: `wiki`, `hushall`, `vanner`, `feedback`, `status`. Reserverade: `recept`, `filer`, `backup`, `kalas`, `schema`, `bokning`. |
| Upptäcktsdokument | `/.well-known/grannskap.json`: vem huset är och hur man når det. |

## 2. Adresser

Ett hus identifieras av sin adress: `https://` + värdnamn (gemener), valfri port, ingen
sökväg, ingen frågesträng, inget avslutande snedstreck. `http://` godtas bara för
`localhost`, `127.0.0.1` och värdnamn som slutar på `.test`, för utveckling.

Alla sökvägar under ett hus:

| Sökväg | Metod | Auth | Syfte |
| --- | --- | --- | --- |
| `/.well-known/grannskap.json` | GET | ingen | Upptäcktsdokument |
| `/api/grannskap/v1/forfragan` | POST | signatur (nyckel från avsändarens upptäcktsdokument) | Be om vänskap |
| `/api/grannskap/v1/forfragan/{id}` | GET | signatur (sparad nyckel) | Status på en förfrågan |
| `/api/grannskap/v1/wiki` | GET | token valfri | Husets wiki (publika avsnitt utan token, alla delade med) |
| `/api/grannskap/v1/hushall` | GET | token | Medlemmar huset delar med just det här vänhuset |
| `/api/grannskap/v1/vanner` | GET | token | Vänners vänner |
| `/api/grannskap/v1/feedback` | POST | token | Lämna en kommentar i husets feedback-låda |
| `/api/grannskap/v1/status` | GET | token | Fråga hur huset mår |
| `/api/grannskap/v1/status` | POST | token | Skicka sin egen status till ett vänhus |

Allt är JSON i UTF-8. Fel svarar `{"error":"text"}` med en HTTP-status som förklarar: 400
felaktigt anrop, 401 okänt hus eller fel signatur, 404 finns inte, 429 vänta, 502 huset bakom
svarar inte.

## 3. Upptäcktsdokument

```json
{
  "grannskap": "1",
  "kind": "hushall",
  "parent": "",
  "name": "Hexance Hem",
  "description": "Ett hushålls egen hemsida: hönshus, inköp, recept, årshjul och gårdsprojekt.",
  "url": "https://hem.hexance.se",
  "public_key": "base64(ed25519 publik nyckel, 32 byte)",
  "discoverable": true,
  "services": [{"key": "wiki", "version": 1}, {"key": "hushall", "version": 1}],
  "endpoints": {"request": "/api/grannskap/v1/forfragan", "status": "/api/grannskap/v1/forfragan/{id}", "wiki": "/api/grannskap/v1/wiki"},
  "rate_limit_per_minute": 30,
  "docs": "https://hem.hexance.se/grannskap",
  "version": "2026-11-04.5aa89e",
  "wiki_updated": "2026-11-04T03:25:14Z",
  "status": "Bygger Grannskap. Hör av er!",
  "time": "2026-11-04T04:06:38Z",
  "status_min_interval_seconds": 600
}
```

Regler: `grannskap`, `name`, `url` och `public_key` är obligatoriska. `url` måste vara samma
adress som dokumentet hämtades från; annars är dokumentet ogiltigt (skydd mot att någon pekar
ut ett annat hus). `discoverable` säger om vänner får visa huset för sina vänner. `version`
byts vid varje driftsättning så vänner ser när sidan uppdaterats. `wiki_updated` är RFC 3339.
`status` är en rad på högst 140 tecken. Dokumentet får cachas i högst 5 minuter.

## 4. Signering

Varje anrop utom hämtning av upptäcktsdokumentet bär tre huvuden:

- `X-Grannskap-House`: avsändarens adress.
- `X-Grannskap-Timestamp`: unix-sekunder när anropet gjordes. Mottagaren avvisar anrop som är
  mer än 5 minuter gamla eller ligger mer än 5 minuter i framtiden.
- `X-Grannskap-Signature`: base64 av en ed25519-signatur över den kanoniska strängen.

Kanonisk sträng (`\n` är ett radbrytningstecken, inte två tecken):

```
METOD\nSÖKVÄG\nTIDSSTÄMPEL\nhex(sha256(kropp))
```

`METOD` i versaler, `SÖKVÄG` är sökvägen utan värdnamn och utan frågesträng (t.ex.
`/api/grannskap/v1/wiki`), `TIDSSTÄMPEL` som decimaltal, kroppen exakt de bytes som skickas
(tom kropp ger sha256 av noll byte, `e3b0c442…`). Testvektorer finns i `testvectors/`.

Verifiering: för `POST /forfragan` hämtar mottagaren avsändarens upptäcktsdokument och använder
nyckeln där (det bevisar att avsändaren kontrollerar adressen). För allt annat används nyckeln
som sparades när huset godkändes; byter ett hus nyckel måste vänskapen göras om.

Tjänsteanrop bär dessutom `Authorization: Grannskap <token>`. Token och signatur måste höra till
samma hus; annars 401.

## 5. Förfrågan och godkännande

1. A läser B:s upptäcktsdokument.
2. A skickar `POST /api/grannskap/v1/forfragan` till B:

   ```json
   {"message": "Hej!", "services_wanted": ["wiki", "hushall"], "token_for_you": "<slumpad token, minst 32 byte>"}
   ```

   `token_for_you` är den token B ska visa när B anropar A. B svarar
   `{"id": 7, "status": "inkommen"}` och visar förfrågan för sin ägare.
3. A frågar `GET /api/grannskap/v1/forfragan/7` högst en gång i timmen. Svaret är
   `{"id": 7, "status": "inkommen|van|avslagen|pausad", "token_for_you": "…", "services": [...]}`.
   `token_for_you` finns med när status är `van`: det är A:s token hos B.
4. Båda sidor sparar: det andra husets adress, namn, typ, publika nyckel, tjänster, token in
   (den de visar hos oss) och token ut (den vi visar hos dem).

Ett hus som fått en förfrågan via ett formulär (utan `token_for_you`) skickar själv en förfrågan
tillbaka när ägaren godkänt, så vänskapen blir ömsesidig.

Status `pausad`: huset avvisar alla anrop från vännen tills ägaren återupptar. Vänskapen kan sägas
upp av båda sidor när som helst; token slutar gälla direkt.

## 6. Tjänster

### wiki (läs)

```json
{"house": {"name": "…", "url": "…", "kind": "hushall"},
 "sections": [{"key": "oversikt", "title": "Översikt", "body": "Text…", "tags": ["go", "postgres"]}]}
```

Publika avsnitt utan token; med token även avsnitt märkta "bara vänner". Högst 30 avsnitt,
titel högst 120 tecken, text högst 6000 tecken, högst 8 taggar om högst 40 tecken.

### hushall (läs, token)

```json
{"house": {...}, "members": [{"name": "Anna", "role": "vuxen", "age": 41, "birthday": "1985-03-02", "wishlist_url": "https://…"}]}
```

Varje fält finns med bara om ägaren delat det med just detta hus. `role` är `vuxen` eller
`barn`. `birthday` är `MM-DD`, eller `YYYY-MM-DD` när även ålder delas. Högst 50 medlemmar.
Barn visas aldrig utan vänskap.

### vanner (läs, token)

```json
{"house": {...}, "friends": [{"name": "…", "url": "https://…", "kind": "forening", "description": "…"}]}
```

Bara hus som ägaren valt att visa, och bara om huset självt är `discoverable`. En väns vän får
bara namn, typ, adress och beskrivning, och måste själv be om vänskap.

### feedback (skriv, token)

`POST {"text": "…"}` (högst 5000 tecken) → `{"status": "mottagen"}`.

### status (fråga och push, token)

`GET` svarar

```json
{"online": true, "version": "…", "status": "…", "wiki_updated": "…", "time": "…", "min_interval_seconds": 600}
```

Det tillfrågade huset avgör hur ofta det svarar samma hus; oftare än `min_interval_seconds`
ger 429 med `{"error": "…", "retry_after_seconds": N}`. `POST` med samma kropp låter ett hus
skicka ut sin status till en vän med eget intervall. Inget hus kollar andra hus automatiskt;
status är en fråga ägaren ställer, eller något huset självt väljer att skicka.

## 7. Fritext från andra hus

Allt som kommer från ett annat hus är fri text skriven av människor eller AI:er man inte styr
över. Ett hus som tar emot den:

1. tvättar den vid mottagning: taggar, skript- och stilblock, styrtecken (< 0x20, 0x7f) och
   osynliga tecken (Unicode-kategorierna Cf, Co, Cs: nollbredd, bidi-byten, privata tecken) tas
   bort; blanksteg slås ihop; längder kapas: namn 80, beskrivning 600, statusrad 140,
   meddelande 2000, wiki-titel 120, wiki-text 6000, synpunkt 5000, tagg 40;
2. visar den som text med escaping, aldrig som HTML eller markdown, och märker att den kommer
   från ett annat hus;
3. godtar länkar bara som `https://` i fält som ska vara länkar, och följer dem aldrig
   automatiskt;
4. behandlar den som data i alla AI-prompter: citerad mellan markörer som säger att den inte
   ska lydas, med markörtecknen borttagna ur själva texten;
5. validerar schemat och kapar listor: 30 wiki-avsnitt, 50 medlemmar, 100 vänner, 20 tjänster;
6. tvättar även felmeddelanden från andra hus innan de visas eller loggas.

## 8. Gränser och logg

Ett hus anger `rate_limit_per_minute` i sitt upptäcktsdokument (vi använder 30) och svarar
429 över gränsen. Förfrågningar från hus som inte är vänner begränsas per IP. Varje inkommande
anrop loggas per hus: tid, sökväg, status. Loggen är ägarens.

## 9. Versioner

Sökvägen bär versionen (`v1`). Nya fält får läggas till i svar utan ny version; mottagare
ignorerar okända fält. Ändras betydelsen av ett fält, eller tas något bort, kommer `v2` bredvid
`v1`, som lever vidare tills alla vänner bytt. `grannskap` i upptäcktsdokumentet är
protokollversionen.
