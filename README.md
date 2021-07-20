## 免密拉取镜像

### Usage

```
apiVersion: tools.51talk.me/v1beta1
kind: CR
metadata:
  name: cr-sample
spec:
  domain: root:pass:hub.baidu.com
  # sa可选, 默认default
  sa: default
  # watchns可选,默认all
  watchns: ddddd
```