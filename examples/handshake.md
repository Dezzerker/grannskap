# Ett helt handslag med curl

Alla anrop utom det första är signerade. Signeringen görs här med ett litet Go-program
(`go run ./examples/sign`) eller med paketet i ert eget språk; se `testvectors/`.

## 1. Läs upptäcktsdokumentet

```bash
curl -s https://testhus.hexance.se/.well-known/grannskap.json
```

Kontrollera att `url` i svaret är `https://testhus.hexance.se` och spara `public_key`.

## 2. Skicka förfrågan

Kroppen:

```json
{"message":"Hej från Exempelhuset!","services_wanted":["wiki","hushall"],"token_for_you":"<32+ slumpade byte, base64url>"}
```

Beräkna `TS=$(date +%s)` och signaturen över

```
POST\n/api/grannskap/v1/forfragan\n<TS>\n<hex sha256 av kroppen>
```

med er privata nyckel, och skicka:

```bash
curl -s -X POST https://testhus.hexance.se/api/grannskap/v1/forfragan \
  -H "Content-Type: application/json" \
  -H "X-Grannskap-House: https://hus.example.se" \
  -H "X-Grannskap-Timestamp: $TS" \
  -H "X-Grannskap-Signature: $SIG" \
  --data-binary @forfragan.json
```

Svar: `{"id":12,"status":"van","token_for_you":"…"}` från Testhuset (det godkänner direkt); ett
vanligt hus svarar `"status":"inkommen"` tills ägaren godkänt.

## 3. Fråga om status

```bash
curl -s https://testhus.hexance.se/api/grannskap/v1/forfragan/12 \
  -H "X-Grannskap-House: https://hus.example.se" \
  -H "X-Grannskap-Timestamp: $TS" \
  -H "X-Grannskap-Signature: $SIG"
```

(signaturen över `GET\n/api/grannskap/v1/forfragan/12\n<TS>\ne3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`, dvs. tom kropp).

## 4. Läs wikin med er token

```bash
curl -s https://testhus.hexance.se/api/grannskap/v1/wiki \
  -H "Authorization: Grannskap $TOKEN" \
  -H "X-Grannskap-House: https://hus.example.se" \
  -H "X-Grannskap-Timestamp: $TS" \
  -H "X-Grannskap-Signature: $SIG"
```

## 5. Testhuset läser er wiki

Inom en minut hämtar Testhuset `https://hus.example.se/api/grannskap/v1/wiki` med den token ni
gav i `token_for_you`, signerat med sin nyckel. Resultatet syns på <https://testhus.hexance.se>
under ert hus: antal avsnitt, eller felet det fick.
