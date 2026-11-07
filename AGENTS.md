# PodConnect — sådan arbejder du her

Én side. Læs kun denne fil, issuet og den kode, issuet berører.

PodConnect giver Home Assistant Spotify-musik på HomePods. To dele:

| Del | Hvor | Rolle |
|---|---|---|
| Control (HA-integration) | `custom_components/podconnect/` | Spotify Web API, `media_player`, services som PodView/PodVoice kalder |
| Speakers (add-on) | `podconnect/manager/` (Go) + go-librespot/OwnTone | Selve Spotify Connect-modtageren og lyden til HomePods |

Brugere og PodView (`BixelVentures/podvoice`) mærker alt, hvad der sker her.

## Arbejdsgang for et issue

1. Formulér brugerresultatet: "Når jeg trykker X, sker Y inden for Z ms."
2. Skriv en test, der fejler nu (`tests/ha_framework` for Control mod rigtig HA,
   `podconnect/manager/*_test.go` for Speakers).
3. Ret i det led, fejlen opstår i. Kør testen og den berørte suite. Grøn = færdig.
4. PR: hvad brugeren oplever nu, hvilken test der beviser det, hvad der ikke kunne afprøves.

## Krav til oplevelsen

- Pause, afspil, næste, forrige, spol, shuffle, repeat og lydstyrke er idempotente
  intents. De må ikke vente på en ny Spotify-poll og må ikke afvises, fordi en poll landede
  imens. Konto, ejer, mål og Speakers-rute tjekkes stadig ved afsendelse.
- Kun start af nyt indhold og flytning mellem rum bekræftes mod aktuel observation.
- Efter en kommando skal tilstanden være opdateret inden for få sekunder (ikke 10 s-poll).
- Ingen tokens, persondata eller rå payloads i logs.

## Det gør du ikke

Status-, plan- eller evidensdokumenter; versionsbump eller release i et fejlrettelses-issue;
ændre en test, så den passer til fejlen.

## Kommandoer

```sh
python3.14 -m venv .venv && .venv/bin/pip install homeassistant==2026.8.2 hassil home-assistant-intents numpy
.venv/bin/python -B -m unittest discover -s tests/ha_framework -p 'test_*.py'
.venv/bin/python -B -m unittest discover -s tests -p 'test_*.py'
(cd podconnect/manager && go test ./...)
```

## Udgivelse (kun når det er bestilt)

Control: bump `custom_components/podconnect/manifest.json` + linje i `CHANGELOG.md`.
Speakers: bump `podconnect/config.yaml` + linje i `CHANGELOG.md`. Se `docs/releasing.md`.
