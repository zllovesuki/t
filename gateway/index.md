# `t`, like ngrok, but ambitious

To learn more about `t`, visit [github.com/zllovesuki/t](https://github.com/zllovesuki/t/).

# Endpoint

You connect to `t` under the domain of
```
{{.Domain}}
```

Using the client for your operating system, and run

```
./client -where {{.Domain}} -forward http://127.0.0.1:3000
```

Now you can have a tunnel for your locally running apps!

```
==================================================

Your Hostname: https://{{.Random}}.{{.Domain}}

==================================================
```