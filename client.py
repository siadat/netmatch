import requests
import random
import time
import sys

actor = sys.argv[1]
key = sys.argv[2]
count = int(sys.argv[3])

while True:
  # actor = "actor%d" % random.randint(0, 1<<20)
  payload = "payload%d" % random.randint(0, 1<<10)
  url = "http://localhost:8000/match?actor=%s&key=%s&payload=%s&count=%d" % (actor, key, payload, count)
  print({
      "key": key,
      "actor": actor,
      "count": count,
      })

  
  try:
    resp = requests.get(url)
  except requests.ConnectionError:
    retry = 1
    print("server not ready, retying in %ds", retry)
    time.sleep(retry)
    continue


  print(resp.text)
  # time.sleep(1)
