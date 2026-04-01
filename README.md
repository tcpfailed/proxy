## SSHProxy
```js
1. apt-get update && sudo apt install -y \
screen git golang-go 
2. git clone https://github.com/tcpfailed/proxy
3. cd proxy
4. go run proxy.go backendip cncscreenport proxyport
```
---

### Screening
1. screen go run proxy.go backendip cncscreenport proxyport
2. <kbd>ctrl + a + d</kbd> (dont use the + key

### Socials & Other:
* GitHub: *https://github.com/tcpfailed*
* Discord: *tcpxd*
* Telegram: *tcpfailed*


### Change Log:
- Resimplified the code after (1yr)
- Removed discord logs (might re add)
- Removed error logs that kept allocating memory and causing the screen / instance to die
