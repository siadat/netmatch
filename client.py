import requests
import random
import time
import sys

actor = sys.argv[1]
event = sys.argv[2]
mates = int(sys.argv[3])

while True:
  # actor = "actor%d" % random.randint(0, 1<<20)
  payload = "payload%d" % random.randint(0, 1<<10)
  url = "http://localhost:8000/event?actor=%s&event=%s&payload=%s&mates=%d" % (actor, event, payload, mates)
  print({
      "actor": actor,
      "event": event,
      "mates": mates,
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
