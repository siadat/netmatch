sleep 5
while sleep 0; do
  curl -s "localhost:8080/event?actor=spec&event=e1"
  # curl -s "localhost:8080/spec?event=e2"
done
