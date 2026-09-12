#!/bin/bash
# Run INSIDE the linux devbox container.
S=$(command -v sync)
echo "sync binary: $S"
sync --version | head -1
W=/tmp/lp
rm -rf $W; mkdir -p $W; cd $W
mkdir d1; touch f1
printf hi > writeonly; chmod 222 writeonly
touch noperm; chmod 000 noperm
mkfifo fifo1
p() {
  echo "### sync $*"
  LC_ALL=C timeout 5 sync "$@" >$W/.o 2>$W/.e
  echo "exit=$? (124=hung)"
  echo "--out--"; cat $W/.o
  echo "--err--"; cat $W/.e
  echo
}
p
p -d
p -f
p --data
p --file-system
p nosuch
p -f nosuch
p -d nosuch
p f1
p -d f1
p -f f1
p d1
p -d d1
p -f d1
p fifo1
p -d fifo1
p -f fifo1
p writeonly
p noperm
p -d noperm
p /dev/zero
p -d /dev/zero
p -f /dev/zero
p -df f1
p ''
p -d ''
p -f ''
p -
p -- f1
p --bogus
p --data=x f1
p -z
echo "=== id ==="; id
