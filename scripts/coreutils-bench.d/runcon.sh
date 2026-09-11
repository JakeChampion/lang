# On a kernel without SELinux runcon does one of two things, and both are
# startup plus one small read. Printing the current context opens and reads
# /proc/self/attr/current; the refusal reads /proc/self/mounts to find out
# there is no selinuxfs. GNU pays the dynamic loader AND dlopens
# libselinux (and libpcre2 behind it) before either, which is the whole
# margin — the same shape `hostid` measures against the NSS modules.
printf 'runcon prints the current context\tn\t{}\n'
printf 'runcon with options and no command\tn\t{} -t x\n'
printf 'runcon refuses on a non-SELinux kernel\tn\t{} ctx /bin/true\n'
printf 'runcon no command specified\tn\t{} ctx\n'
