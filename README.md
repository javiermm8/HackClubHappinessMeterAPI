# Hack Club Happiness Meter API

_A simple API for logging the happiness of users in the hackclub
            community._ 


![Hackatime Badge](https://hackatime.hackclub.com/api/v1/badge/U0B5ZBRJTL4/javiermm8/HackClubHappinessMeterAPI)


Detailed information and example requests can be found in: `https://pep-unethical-copy.ngrok-free.dev/docs/`

## Base URL: 

`https://pep-unethical-copy.ngrok-free.dev/`

## Features

- Authorization
- SQLite Database
- External API (Slack integration)
- Rate limiting
- Proper error handling & input validation (Arguable, but I think it's good enough.)
- Seven endpoints (2 POST and 4 GET + /slackEvents)

## Authorization

Some endpoints require you to provide an APIKey. You can register and get one by sending "Register" to HackClubHappinessMeterAPI Helper(ID: U0B6D2WBM8B) on Slack. The bot will reply with your APIKey, keep it safe!

**Note for reviewers:** Reviewers are encouraged to follow the usual registration process through the Slack bot but, if you prefer to stay anonymous or simply don't want to deal with that, you can find a reviewer key in the project submission notes. Just be aware that(due to my laziness) a reviewer key might be able to create entries on behalf of other users or check other user's profiles, so be careful. 

## Endpoints
| Endpoint    | Method      | Description |
| ----------- | ----------- | ----------- |
| /status     | GET         | Check if the API is running. |
| /docs       | GET         | Returns the index.html for the docs page. |
| /happinessFriend | GET       |  Returns the second latest user(to avoid finding yourself) with the provided happiness level (Max:10/Min:1). |
| /profile   | GET        | Returns your profile. Includes: some stats, user info and latest entry. Requires APIKey and SlackID. |
| /newEntry      | POST       | Adds a new entry. Requires APIKey, HappinessLevel, SlackID and Note. All information provided exept the APIKey can be publicly accesed via the /happinessFriend endpoint, so be careful with what you share. Any misuse of the "Note" feature will result in a ban(I'll just randomize your API Key in the database).        |
| /newUser   | POST        | Management endpoint for manually adding new users. Requires ManagementKey and SlackID. |

(There's also POST /slackEvents but that is intended to be used only by Slack.)

## Error Handling

All errors are marked with an http status error code and have a clear message explaining what's wrong. In the case of an internal server error (such as when something is wrong with the database) the message will also politely ask you to contact me. 

## Rate Limits

1 request/second per ip address.

## Slack

The API is dependent on Slack for multiple features, in the case of a Slack outage this API would be affected.

## Security warnings

After some careful reviewing of the API I learnt that:

- CORS wildcards are bad, especially for authenticated endpoints. I'll change it if anyone wants to make a frontend.
- The database is vulnerable to rainbow-table attacks because I didn't salt the APIs when hashing them. This would only be a problem if the database were to leak, but honestly, if that happens, I'd have bigger problems than leaking some happiness levels.

## Host it yourself

If you wan't to host your own version of this API you just have clone this repository, change the following things:
- Create a .env file with the necessary credentials listed in .env.example
- In main.go, change the listening port if needed(default 8081) or the rate limiter.
Then, build it with: 
`go build .`
And run it!

## Created by javim.
