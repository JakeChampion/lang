# pinky reads /var/run/utmp and nothing else — there is no FILE operand
# to point it at a fixture — so what is measured here is what the machine
# has, which on a build container is no database at all. That makes these
# startup numbers rather than throughput ones, and the long format the
# only workload whose input is under our control: it is /etc/passwd and
# the two dot-files under the home directory it names.
printf 'pinky\tn\t{}\n'
printf 'pinky -q\tn\t{} -q\n'
printf 'pinky filtered to one user\tn\t{} root\n'
printf 'pinky -l of one user\tn\t{} -l root\n'
printf 'pinky -l of eight users\tn\t{} -l root daemon bin sys man lp mail news\n'
