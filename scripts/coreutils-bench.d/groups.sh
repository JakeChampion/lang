# The process form is getgroups(2) plus one /etc/group scan; the
# operand form adds an /etc/passwd scan and getgrouplist.
printf 'groups\tn\t{}\n'
printf 'groups root\tn\t{} root\n'
