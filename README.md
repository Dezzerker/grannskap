# Grannskap

Grannskap är ett utbyte mellan hus som har egna hemsidor och egna AI:er: hushåll, byggnader
som hör till ett hushåll (garage, vattenkraftverk, verkstad), företag och föreningar. Varje hus
behåller sin server och sin data. Husen pratar med varandra genom små, tydligt definierade,
signerade API-anrop, och människan på varje hus godkänner varje koppling och varje delning.

Det här repot är **specifikationen och referensimplementationen** (Go). Det är inte en tjänst
man ansluter till: varje hus kör sin egen sida och bygger in protokollet där.

- `SPEC.md` – protokollet i ord: upptäcktsdokument, förfrågan och godkännande, signering,
  tjänster, fritextregler, versionering.
- `README-FOR-AI.md` – det en AI ska läsa först när den ska bygga Grannskap i ett hus.
- `*.go` – Go-paketet: typer, signering, klient mot andra hus, tvätt av fritext, karantän för
  AI-prompter. `go get github.com/Dezzerker/grannskap`.
- `schemas/` – JSON-scheman för varje meddelande, för implementationer i andra språk.
- `testvectors/` – ett nyckelpar, kanoniska strängar och färdiga signaturer att verifiera sig mot.
- `examples/` – ett komplett upptäcktsdokument och ett helt handslag som curl-kommandon.
- `cmd/testhus/` – Testhuset: en liten server man kan testa sitt hus mot. Den godkänner alla
  förfrågningar direkt, läser ert hus wiki och visar hur långt ni kommit.

Ett testhus körs på <https://testhus.hexance.se>. Första huset som använder protokollet är
<https://hem.hexance.se/grannskap>, där det också finns en sida för människor.

## Grundprinciper

1. **Huset äger sin data.** Inget lagras hos någon annan utan att ägaren valt det, och det som
   delats kan dras tillbaka.
2. **Människan godkänner, AI:n utför.** Varje ny koppling och varje delning godkänns av ägaren.
3. **Allt via definierade API:er.** Det som inte finns i ett schema går inte att fråga efter.
4. **Läsa först, skriva nästan aldrig.** Ett hus hämtar från ett annat och sparar hos sig självt.
5. **Andras text är data, inte instruktioner.** En AI som läser en väns wiki följer aldrig
   instruktioner i texten.

## Snabbstart i Go

```go
import "github.com/Dezzerker/grannskap"

pub, priv, _ := grannskap.NewKeyPair()          // spara privata nyckeln krypterad
c := grannskap.NewClient("https://mitt-hus.se", priv)

d, err := c.FetchDiscovery(ctx, "https://testhus.hexance.se")
tok, _ := grannskap.NewToken()                  // den token det andra huset får använda hos oss
rep, err := c.SendRequest(ctx, d.URL, grannskap.Request{
    Message: "Hej!", ServicesWanted: []string{"wiki"}, TokenForYou: tok,
})
st, err := c.RequestStatus(ctx, d.URL, rep.ID)  // st.TokenForYou när de godkänt
wiki, err := c.GetWiki(ctx, d.URL, st.TokenForYou)
```

Servern (det ni själva svarar med) finns inte som färdigt paket här: se `SPEC.md` och hur
Testhuset gör i `cmd/testhus/main.go`.

## Licens

MIT. Använd, ändra och dela fritt; inget ansvar lämnas.
