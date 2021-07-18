# `t`, like ngrok, but ambitious

To learn more about `t`, visit [github.com/zllovesuki/t](https://github.com/zllovesuki/t/).

# Endpoint

You are visiting the `t` instance under the owner of
```
{{.Domain}}
```

Using the client for your operating system, and run

```
./client -where {{.Domain}} -forward http://127.0.0.1:{{.Port}}
```

Now you can have a tunnel for your locally running apps!

```
==================================================

Your Hostname: https://{{.Random}}.{{.Domain}}

Requests will be forwarded to: http://127.0.0.1:{{.Port}}

==================================================
```