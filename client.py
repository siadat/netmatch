import requests
import random
import time
import sys
import urllib

key = sys.argv[1]
labels = sys.argv[2]
selector = sys.argv[3]
count = int(sys.argv[4])

while True:
  payload = "payload%d" % random.randint(0, 1<<10)
  
  url = "http://localhost:8000/match?labels=%s&selector=%s&key=%s&payload=%s&count=%d" % (
          urllib.quote(labels),
          urllib.quote(selector),
          urllib.quote(key),
          urllib.quote(payload),
          count,
          )
  # print({
  #     "key": key,
  #     "labels": labels,
  #     "count": count,
  #     })

  
  try:
    resp = requests.get(url)
  except requests.ConnectionError:
    retry = 1
    print("server not ready, retying in %ds", retry)
    time.sleep(retry)
    continue


  # print(resp.text)
  # time.sleep(1)
