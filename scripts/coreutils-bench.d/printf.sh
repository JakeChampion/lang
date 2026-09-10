printf "printf '%%s\\\\n' x\tn\t{} '%%s\\\\n' x\n"
printf "printf '%%d %%s %%5.2f\\\\n' 1 a 2.5\tn\t{} '%%d %%s %%5.2f\\\\n' 1 a 2.5\n"
printf "printf '%%s\\\\n' cycling over 2000 operands\tn\t{} '%%s\\\\n' %s\n" "$(seq 2000 | tr '\n' ' ')"
printf "printf '%%.20f %%e %%g' cycling over 300 floats\tn\t{} '%%.20f %%e %%g\\\\n' %s\n" "$(seq 1 0.37 111 | tr '\n' ' ')"
