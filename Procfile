netmatch: go run -race cmd/netmatch/main.go :8000

# client1: python raft.py node1
# client2: python raft.py node2
# client3: python raft.py node3
# client4: python raft.py node4
# client5: python raft.py node5

client1: python client.py e n=1 n!=1 1
client2: python client.py e n=2 n!=2 1
client3: python client.py e n=3 n!=3 1
client4: python client.py e n=6 n!=6 3
