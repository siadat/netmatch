sleep 5
while sleep 1; do
  curl -s "localhost:8080/impl?event=e1"
  # curl -s "localhost:8080/impl?event=e2"
done
